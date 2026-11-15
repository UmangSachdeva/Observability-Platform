// Package observability is a small Go client for the Observability Platform
// API. It signs events with a project API key and sends them from a
// background goroutine, so reporting never blocks the caller.
package observability

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Event is one error report. Empty Service, Environment, Level and Timestamp
// are filled from the client configuration and the current time.
type Event struct {
	Timestamp   time.Time         `json:"timestamp"`
	Service     string            `json:"service"`
	Environment string            `json:"environment,omitempty"`
	Level       string            `json:"level,omitempty"`
	Message     string            `json:"message"`
	StackTrace  string            `json:"stack_trace,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	// TraceID and SpanID link the event to a trace. ReportContext fills them
	// from the active span.
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`

	// parent is the span that reported the event; sending the event becomes
	// its child so the API's spans join the same trace.
	parent trace.SpanContext
}

type Config struct {
	// Endpoint is the API base URL, for example http://localhost:8080.
	Endpoint string
	// AccessKey and SecretKey come from
	// POST /v1/accounts/{accountID}/projects/{projectID}/keys. The key decides
	// which project events are stored in.
	AccessKey string
	SecretKey string
	// Service and Environment are applied to events that leave them empty.
	Service     string
	Environment string
	// QueueSize is how many events may wait to be sent (default 1000).
	QueueSize int
	// HTTPClient sends the requests (default: 5 second timeout).
	HTTPClient *http.Client
	// OnError is called from the background goroutine when an event cannot
	// be delivered.
	OnError func(error)
}

type Client struct {
	config     Config
	url        string
	requestURI string
	queue      chan Event
	done       chan struct{}

	mu     sync.RWMutex
	closed bool
}

func New(config Config) (*Client, error) {
	if config.AccessKey == "" || config.SecretKey == "" {
		return nil, errors.New("observability: AccessKey and SecretKey are required")
	}
	if strings.TrimSpace(config.Service) == "" {
		return nil, errors.New("observability: Service is required")
	}
	endpoint, err := url.Parse(strings.TrimRight(config.Endpoint, "/") + "/v1/errors")
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("observability: invalid Endpoint %q", config.Endpoint)
	}
	if config.QueueSize <= 0 {
		config.QueueSize = 1000
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	client := &Client{
		config:     config,
		url:        endpoint.String(),
		requestURI: endpoint.RequestURI(),
		queue:      make(chan Event, config.QueueSize),
		done:       make(chan struct{}),
	}
	go client.run()
	return client, nil
}

// Report queues an event without blocking. It returns false when the queue
// is full or the client is closed; the event is then dropped.
func (c *Client) Report(event Event) bool {
	if event.Service == "" {
		event.Service = c.config.Service
	}
	if event.Environment == "" {
		event.Environment = c.config.Environment
	}
	if event.Level == "" {
		event.Level = "error"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return false
	}
	select {
	case c.queue <- event:
		return true
	default:
		return false
	}
}

// CaptureError reports err as an "error" level event.
func (c *Client) CaptureError(err error, attributes map[string]string) bool {
	return c.Report(Event{Level: "error", Message: err.Error(), Attributes: attributes})
}

// ReportContext reports event linked to the active span in ctx: the event
// gets the span's trace_id and span_id, and the span records the problem
// (an exception event and Error status for "error" and "fatal", a plain span
// event for other levels).
func (c *Client) ReportContext(ctx context.Context, event Event) bool {
	span := trace.SpanFromContext(ctx)
	if sc := span.SpanContext(); sc.IsValid() {
		event.TraceID, event.SpanID, event.parent = sc.TraceID().String(), sc.SpanID().String(), sc
		level := event.Level
		if level == "" {
			level = "error"
		}
		if level == "error" || level == "fatal" {
			attributes := []attribute.KeyValue{attribute.String("exception.message", event.Message)}
			if event.StackTrace != "" {
				attributes = append(attributes, attribute.String("exception.stacktrace", event.StackTrace))
			}
			span.AddEvent("exception", trace.WithAttributes(attributes...))
			span.SetStatus(codes.Error, event.Message)
		} else {
			span.AddEvent(event.Message, trace.WithAttributes(attribute.String("event.level", level)))
		}
	}
	return c.Report(event)
}

// CaptureErrorContext reports err as an "error" event linked to the active span.
func (c *Client) CaptureErrorContext(ctx context.Context, err error, attributes map[string]string) bool {
	return c.ReportContext(ctx, Event{Level: "error", Message: err.Error(), Attributes: attributes})
}

// Close stops accepting events and waits until queued events are sent or ctx
// ends. Call it before the program exits.
func (c *Client) Close(ctx context.Context) error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.queue)
	}
	c.mu.Unlock()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("observability: events still queued at shutdown: %w", ctx.Err())
	}
}

func (c *Client) run() {
	defer close(c.done)
	for event := range c.queue {
		if err := c.send(event); err != nil && c.config.OnError != nil {
			c.config.OnError(err)
		}
	}
}

// send signs the exact body bytes as the API expects:
// HMAC-SHA256(secret, "<unix seconds>\nPOST\n<path?query>\n<hex sha256(body)>").
func (c *Client) send(event Event) (err error) {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("observability: encode event: %w", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	bodyHash := sha256.Sum256(body)
	canonical := timestamp + "\n" + http.MethodPost + "\n" + c.requestURI + "\n" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, []byte(c.config.SecretKey))
	_, _ = mac.Write([]byte(canonical))

	ctx := context.Background()
	if event.parent.IsValid() {
		// Continue the reporting span's trace, so the API's handling of this
		// event shows up under it.
		var span trace.Span
		ctx, span = otel.Tracer("observability-platform/sdk").Start(
			trace.ContextWithSpanContext(ctx, event.parent), "POST /v1/errors", trace.WithSpanKind(trace.SpanKindClient))
		defer span.End()
		defer func() {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
		}()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("observability: build request: %w", err)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Access-Key", c.config.AccessKey)
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := c.config.HTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("observability: send event: %w", err)
	}
	defer response.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("observability: server returned %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}
	return nil
}

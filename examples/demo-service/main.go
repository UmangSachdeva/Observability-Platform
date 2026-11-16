// Command demo-service is a tiny shop backend that reports its failures to the
// Observability Platform through pkg/observability. Some endpoints fail on
// purpose so the integration can be checked end to end.
//
//	OBS_ENDPOINT=http://localhost:8080 OBS_ACCESS_KEY=ak_... OBS_SECRET_KEY=... \
//	  go run ./examples/demo-service
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"observability-platform/pkg/observability"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := observability.Config{
		Endpoint:    env("OBS_ENDPOINT", "http://localhost:8080"),
		AccessKey:   os.Getenv("OBS_ACCESS_KEY"),
		SecretKey:   os.Getenv("OBS_SECRET_KEY"),
		Service:     "demo-shop",
		Environment: env("OBS_ENVIRONMENT", "demo"),
		OnError:     func(err error) { logger.Error("could not report event", "error", err) },
	}
	obs, err := observability.New(config)
	if err != nil {
		logger.Error("observability client setup failed", "error", err)
		os.Exit(1)
	}
	// Spans go to the same project as the error events (OTLP /v1/traces).
	shutdownTracing, err := observability.StartTracing(context.Background(), config)
	if err != nil {
		logger.Error("tracing setup failed", "error", err)
		os.Exit(1)
	}

	shop := &shop{obs: obs}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /checkout", shop.checkout)
	mux.HandleFunc("GET /orders/{id}", shop.order)
	mux.HandleFunc("GET /inventory", shop.inventory)
	mux.HandleFunc("GET /panic", shop.crash)

	server := &http.Server{
		Addr:              env("DEMO_ADDR", ":9090"),
		Handler:           obs.InstrumentMux(mux), // spans per route + panic reporting
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("demo service listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("demo service failed", "error", err)
	}
	// Send everything still queued before exiting.
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := obs.Close(closeCtx); err != nil {
		logger.Error("observability flush failed", "error", err)
	}
	// After the events, so the spans of their delivery are exported too.
	if err := shutdownTracing(closeCtx); err != nil {
		logger.Error("tracing flush failed", "error", err)
	}
}

var tracer = otel.Tracer("demo-shop")

type shop struct {
	obs *observability.Client
}

const checkoutLimit = 1000

// checkout declines amounts over the limit and reports it as a warning.
func (s *shop) checkout(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("amount")
	amount, err := strconv.ParseFloat(raw, 64)
	if err != nil || amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be a positive number"})
		return
	}
	orderID := strconv.FormatInt(time.Now().UnixNano(), 36)
	_, charge := tracer.Start(r.Context(), "charge card", trace.WithAttributes(attribute.String("checkout.amount", raw)))
	time.Sleep(3 * time.Millisecond) // the payment provider
	if amount > checkoutLimit {
		charge.AddEvent("card declined", trace.WithAttributes(attribute.String("decline.reason", "amount exceeds limit")))
	}
	charge.End()
	if amount > checkoutLimit {
		s.obs.ReportContext(r.Context(), observability.Event{
			Level:      "warning",
			Message:    "card declined: amount exceeds limit",
			Attributes: map[string]string{"amount": raw, "order_id": orderID},
		})
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": "card declined", "order_id": orderID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paid", "order_id": orderID})
}

// order reports ids that are not numbers.
func (s *shop) order(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := strconv.Atoi(id); err != nil {
		s.obs.CaptureErrorContext(r.Context(), fmt.Errorf("invalid order id %q: %w", id, err), map[string]string{"path": r.URL.Path})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "order id must be a number"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "shipped"})
}

// inventory calls a dependency that is slower than its timeout.
func (s *shop) inventory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
	defer cancel()
	ctx, span := tracer.Start(ctx, "GET inventory-svc/stock", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("server.address", "inventory-svc")))
	if err := slowInventoryLookup(ctx); err != nil {
		// Reported from the CLIENT span, so the error points at the slow call.
		s.obs.CaptureErrorContext(ctx, fmt.Errorf("inventory service timeout: %w", err), map[string]string{"dependency": "inventory"})
		span.End()
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "inventory unavailable"})
		return
	}
	span.End()
	writeJSON(w, http.StatusOK, map[string]int{"in_stock": 12})
}

func slowInventoryLookup(ctx context.Context) error {
	select {
	case <-time.After(200 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// crash writes to a nil map; the observability middleware recovers the panic.
func (s *shop) crash(w http.ResponseWriter, _ *http.Request) {
	var cache map[string]int
	cache["visits"]++
	writeJSON(w, http.StatusOK, cache)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

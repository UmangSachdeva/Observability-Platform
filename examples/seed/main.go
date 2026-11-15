// Command seed fills a running Observability Platform API with realistic demo
// data: a "storefront" project whose services (api-gateway, checkout-api,
// payments-api, inventory-api, notification-worker) report recurring errors
// with Go, JavaScript and Python stack traces, and the OpenTelemetry traces
// those errors happened in; plus a smaller "search-service" project.
// Timestamps are spread over the last three days.
//
//	go run ./examples/seed -api http://localhost:8080                         # new demo user
//	go run ./examples/seed -api http://localhost:8080 -login you@example.com -password '...'
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	kindInternal = 1
	kindServer   = 2
	kindClient   = 3
	kindProducer = 4
	kindConsumer = 5
)

type api struct {
	base  string
	token string
	http  *http.Client
}

func main() {
	base := flag.String("api", "http://localhost:8080", "Observability Platform API base URL")
	login := flag.String("login", "", "log in as this username or email instead of signing up")
	password := flag.String("password", "demo-password-2026", "password (for -login, or for the new demo user)")
	username := flag.String("username", "demo", "username for the new demo user")
	email := flag.String("email", "demo@tracewell.dev", "email for the new demo user")
	flag.Parse()
	a := &api{base: strings.TrimRight(*base, "/"), http: &http.Client{Timeout: 20 * time.Second}}

	var session struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	}
	if *login != "" {
		a.must("POST", "/v1/auth/login", map[string]string{"login": *login, "password": *password}, &session, http.StatusOK)
	} else {
		a.must("POST", "/v1/auth/signup", map[string]string{"username": *username, "email": *email, "password": *password}, &session, http.StatusCreated)
	}
	a.token = session.AccessToken
	account := session.AccountID

	storefront := a.project(account, "storefront", "Customer-facing shop: gateway, checkout, payments, inventory and notifications")
	search := a.project(account, "search-service", "Product search and indexing")

	now := time.Now().UTC()
	errorsSent, spansSent := seedStorefront(a, storefront, now)
	searchErrors, searchSpans := seedSearch(a, search, now)

	fmt.Printf("Seeded %d error occurrences and %d spans.\n", errorsSent+searchErrors, spansSent+searchSpans)
	if *login == "" {
		fmt.Printf("Sign in as %s / %s\n", *email, *password)
	}
}

type projectKey struct {
	id, access, secret string
}

func (a *api) project(account, name, description string) projectKey {
	var project struct {
		ID string `json:"id"`
	}
	status, body := a.call("POST", "/v1/accounts/"+account+"/projects", map[string]string{"name": name, "description": description}, &project)
	if status == http.StatusConflict {
		var list struct {
			Projects []struct{ ID, Name string } `json:"projects"`
		}
		a.must("GET", "/v1/accounts/"+account+"/projects", nil, &list, http.StatusOK)
		for _, p := range list.Projects {
			if p.Name == name {
				project.ID = p.ID
			}
		}
	} else if status != http.StatusCreated {
		fail("create project %s: %d %s", name, status, body)
	}
	var key struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	}
	a.must("POST", "/v1/accounts/"+account+"/projects/"+project.ID+"/keys", map[string]string{"client_name": name + "-seed"}, &key, http.StatusCreated)
	return projectKey{id: project.ID, access: key.AccessKey, secret: key.SecretKey}
}

// ---------- traces

type spanEvent struct {
	name  string
	at    time.Time
	attrs map[string]string
}

type span struct {
	traceID, spanID, parentID string
	service, name             string
	kind                      int
	start, end                time.Time
	attrs                     map[string]string
	errMessage                string
	events                    []spanEvent
}

type trace struct {
	id    string
	spans []*span
}

func newTrace() *trace { return &trace{id: randomHex(16)} }

func (t *trace) add(parent *span, service, name string, kind int, start time.Time, duration time.Duration, attrs map[string]string) *span {
	s := &span{traceID: t.id, spanID: randomHex(8), service: service, name: name, kind: kind, start: start, end: start.Add(duration), attrs: attrs}
	if parent != nil {
		s.parentID = parent.spanID
	}
	t.spans = append(t.spans, s)
	return s
}

func (s *span) fail(message string, exceptionType, stack string) {
	s.errMessage = message
	attrs := map[string]string{"exception.message": message, "exception.type": exceptionType}
	if stack != "" {
		attrs["exception.stacktrace"] = stack
	}
	s.events = append(s.events, spanEvent{name: "exception", at: s.end.Add(-time.Millisecond), attrs: attrs})
}

func ms(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func jitter(base float64) float64 { return base * (0.7 + mrand.Float64()*0.6) }

// checkoutTrace builds POST /v1/checkout across the gateway, checkout,
// payments and notification services. mode picks what goes wrong and the
// returned span is where it happened.
func checkoutTrace(at time.Time, mode string) (*trace, *span) {
	t := newTrace()
	status := "201"
	payDuration := jitter(140)
	switch mode {
	case "payment-timeout":
		status, payDuration = "504", 3010
	case "declined":
		status = "402"
	case "cart-type-error", "redis-exhausted", "invalid-order":
		status = "500"
		if mode == "invalid-order" {
			status = "400"
		}
		payDuration = 0
	case "rate-limited":
		status = "429"
	}
	total := 22 + payDuration + jitter(18)
	if mode == "rate-limited" {
		total = jitter(4)
	}
	gw := t.add(nil, "api-gateway", "POST /v1/checkout", kindServer, at, ms(total), map[string]string{
		"http.request.method": "POST", "http.route": "/v1/checkout", "http.response.status_code": status, "client.address": "203.0.113." + strconv.Itoa(mrand.IntN(200)+10),
	})
	if mode == "rate-limited" {
		gw.events = append(gw.events, spanEvent{name: "rate limit exceeded", at: at.Add(ms(2)), attrs: map[string]string{"limit": "100/min"}})
		return t, gw
	}
	gwClient := t.add(gw, "api-gateway", "POST checkout-api/checkout", kindClient, at.Add(ms(1.5)), ms(total-3), map[string]string{"server.address": "checkout-api"})
	co := t.add(gwClient, "checkout-api", "POST /checkout", kindServer, at.Add(ms(2.5)), ms(total-5), map[string]string{"http.route": "/checkout", "http.response.status_code": status})
	validate := t.add(co, "checkout-api", "validate cart", kindInternal, at.Add(ms(3)), ms(jitter(2.5)), map[string]string{"cart.items": strconv.Itoa(mrand.IntN(5) + 1)})
	redis := t.add(co, "checkout-api", "redis GET cart", kindClient, at.Add(ms(6)), ms(jitter(1.8)), map[string]string{"db.system.name": "redis", "db.operation.name": "GET"})
	var where *span
	switch mode {
	case "cart-type-error":
		validate.fail("TypeError: Cannot read properties of undefined (reading 'items')", "TypeError", jsStack)
		co.errMessage = "internal error"
		return t, validate
	case "redis-exhausted":
		redis.end = redis.start.Add(ms(15))
		redis.fail("redis: connection pool exhausted", "redis.PoolTimeoutError", "")
		co.errMessage = "internal error"
		return t, redis
	case "invalid-order":
		validate.events = append(validate.events, spanEvent{name: "invalid order id", at: validate.end, attrs: map[string]string{"order.id": "abc"}})
		return t, validate
	}
	payClient := t.add(co, "checkout-api", "POST payments-api/charge", kindClient, at.Add(ms(10)), ms(payDuration+4), map[string]string{"server.address": "payments-api"})
	pay := t.add(payClient, "payments-api", "POST /charge", kindServer, at.Add(ms(11)), ms(payDuration+2), map[string]string{"http.route": "/charge", "http.response.status_code": map[bool]string{true: status, false: "200"}[mode != ""]})
	t.add(pay, "payments-api", "mongo find customers", kindClient, at.Add(ms(12)), ms(jitter(3)), map[string]string{"db.system.name": "mongodb", "db.operation.name": "find", "db.collection.name": "customers"})
	stripe := t.add(pay, "payments-api", "POST api.stripe.com/v1/charges", kindClient, at.Add(ms(16)), ms(payDuration-8), map[string]string{"server.address": "api.stripe.com", "http.request.method": "POST"})
	switch mode {
	case "payment-timeout":
		stripe.fail("payment provider timeout after 3000ms: context deadline exceeded", "context.deadlineExceededError", goTimeoutStack)
		pay.errMessage, co.errMessage, gw.errMessage = "gateway timeout", "upstream timeout", "upstream timeout"
		return t, stripe
	case "declined":
		stripe.attrs["http.response.status_code"] = "402"
		stripe.events = append(stripe.events, spanEvent{name: "card declined", at: stripe.end, attrs: map[string]string{"decline_code": "insufficient_funds"}})
		where = pay
	default:
		stripe.attrs["http.response.status_code"] = "200"
	}
	if mode == "" {
		t.add(pay, "payments-api", "mongo insert payments", kindClient, at.Add(ms(payDuration+8)), ms(jitter(4)), map[string]string{"db.system.name": "mongodb", "db.operation.name": "insert", "db.collection.name": "payments"})
		t.add(co, "checkout-api", "mongo insert orders", kindClient, at.Add(ms(payDuration+15)), ms(jitter(4)), map[string]string{"db.system.name": "mongodb", "db.operation.name": "insert", "db.collection.name": "orders"})
		publish := t.add(co, "checkout-api", "publish order.created", kindProducer, at.Add(ms(payDuration+20)), ms(jitter(1.5)), map[string]string{"messaging.system": "kafka", "messaging.destination.name": "orders"})
		consume := t.add(publish, "notification-worker", "process order.created", kindConsumer, at.Add(ms(total+jitter(40))), ms(jitter(35)), map[string]string{"messaging.system": "kafka"})
		t.add(consume, "notification-worker", "POST api.sendgrid.com/v3/mail/send", kindClient, consume.start.Add(ms(5)), ms(jitter(25)), map[string]string{"server.address": "api.sendgrid.com"})
		where = consume
	}
	return t, where
}

// notificationFailure is the worker's KeyError on an order without an address.
func notificationTrace(at time.Time) (*trace, *span) {
	t := newTrace()
	consume := t.add(nil, "notification-worker", "process order.created", kindConsumer, at, ms(jitter(12)), map[string]string{"messaging.system": "kafka", "messaging.destination.name": "orders"})
	consume.fail("KeyError: 'shipping_address'", "KeyError", pythonStack)
	return t, consume
}

// productTrace builds GET /v1/products/{id}; panic makes the inventory
// service crash on an out-of-range variant.
func productTrace(at time.Time, panicked bool) (*trace, *span) {
	t := newTrace()
	status := "200"
	if panicked {
		status = "500"
	}
	total := jitter(28)
	gw := t.add(nil, "api-gateway", "GET /v1/products/{id}", kindServer, at, ms(total), map[string]string{"http.request.method": "GET", "http.route": "/v1/products/{id}", "http.response.status_code": status})
	client := t.add(gw, "api-gateway", "GET inventory-api/products/{id}", kindClient, at.Add(ms(1)), ms(total-2), map[string]string{"server.address": "inventory-api"})
	inv := t.add(client, "inventory-api", "GET /products/{id}", kindServer, at.Add(ms(2)), ms(total-4), map[string]string{"http.route": "/products/{id}", "http.response.status_code": status})
	t.add(inv, "inventory-api", "mongo find products", kindClient, at.Add(ms(3)), ms(jitter(6)), map[string]string{"db.system.name": "mongodb", "db.operation.name": "find", "db.collection.name": "products"})
	if panicked {
		inv.fail("panic: runtime error: index out of range [3] with length 3", "runtime.boundsError", goPanicStack)
		gw.errMessage = "upstream error"
		return t, inv
	}
	t.add(inv, "inventory-api", "redis GET stock", kindClient, at.Add(ms(10)), ms(jitter(2)), map[string]string{"db.system.name": "redis"})
	return t, inv
}

// ---------- errors

type errorSpec struct {
	service, environment, level, message, stack string
	count                                       int
	window                                      time.Duration
	trace                                       func(time.Time) (*trace, *span)
	attrs                                       func(i int) map[string]string
}

var releases = []string{"v2.3.0", "v2.3.1", "v2.3.1", "v2.3.1"}
var regions = []string{"eu-west-1", "eu-west-1", "us-east-1", "ap-south-1"}

func commonAttrs(i int) map[string]string {
	return map[string]string{"release": releases[i%len(releases)], "region": regions[mrand.IntN(len(regions))], "host": "pod-" + strconv.Itoa(mrand.IntN(6)+1)}
}

func seedStorefront(a *api, key projectKey, now time.Time) (int, int) {
	checkout := func(mode string) func(time.Time) (*trace, *span) {
		return func(at time.Time) (*trace, *span) { return checkoutTrace(at, mode) }
	}
	specs := []errorSpec{
		{"payments-api", "production", "error", "payment provider timeout after 3000ms: context deadline exceeded", goTimeoutStack, 23, 72 * time.Hour, checkout("payment-timeout"),
			func(i int) map[string]string {
				m := commonAttrs(i)
				m["provider"] = "stripe"
				m["order_id"] = "ord_" + randomHex(4)
				return m
			}},
		{"checkout-api", "production", "error", "TypeError: Cannot read properties of undefined (reading 'items')", jsStack, 14, 30 * time.Hour, checkout("cart-type-error"),
			func(i int) map[string]string {
				m := commonAttrs(i)
				m["release"] = "v2.3.1"
				m["cart_id"] = "cart_" + randomHex(4)
				return m
			}},
		{"payments-api", "production", "warning", "card declined: insufficient funds", "", 41, 72 * time.Hour, checkout("declined"),
			func(i int) map[string]string { m := commonAttrs(i); m["decline_code"] = "insufficient_funds"; return m }},
		{"inventory-api", "production", "fatal", "panic: runtime error: index out of range [3] with length 3", goPanicStack, 3, 20 * time.Hour,
			func(at time.Time) (*trace, *span) { return productTrace(at, true) }, commonAttrs},
		{"checkout-api", "production", "error", "redis: connection pool exhausted", "", 8, 12 * time.Hour, checkout("redis-exhausted"), commonAttrs},
		{"checkout-api", "staging", "error", "redis: connection pool exhausted", "", 5, 48 * time.Hour, checkout("redis-exhausted"), commonAttrs},
		{"notification-worker", "production", "error", "KeyError: 'shipping_address'", pythonStack, 6, 60 * time.Hour,
			notificationTrace, func(i int) map[string]string { m := commonAttrs(i); m["queue"] = "orders"; return m }},
		{"api-gateway", "production", "warning", "rate limit exceeded for /v1/checkout", "", 17, 24 * time.Hour, checkout("rate-limited"),
			func(i int) map[string]string {
				m := commonAttrs(i)
				m["client_ip"] = "198.51.100." + strconv.Itoa(mrand.IntN(9)+1)
				return m
			}},
		{"checkout-api", "production", "error", `invalid order id "abc": strconv.Atoi: parsing "abc": invalid syntax`, "", 2, 40 * time.Hour, checkout("invalid-order"), commonAttrs},
	}
	var traces []*trace
	occurrences := 0
	for _, spec := range specs {
		for _, at := range spreadTimes(now, spec.window, spec.count) {
			t, where := spec.trace(at)
			traces = append(traces, t)
			a.sendError(key, map[string]any{
				"service": spec.service, "environment": spec.environment, "level": spec.level,
				"message": spec.message, "stack_trace": spec.stack, "timestamp": where.end.Format(time.RFC3339Nano),
				"attributes": spec.attrs(occurrences), "trace_id": t.id, "span_id": where.spanID,
			})
			occurrences++
		}
	}
	// Healthy traffic around the failures, mostly in the last six hours.
	for _, at := range spreadTimes(now, 6*time.Hour, 60) {
		if mrand.IntN(3) == 0 {
			t, _ := productTrace(at, false)
			traces = append(traces, t)
		} else {
			t, _ := checkoutTrace(at, "")
			traces = append(traces, t)
		}
	}
	return occurrences, a.sendTraces(key, traces)
}

func seedSearch(a *api, key projectKey, now time.Time) (int, int) {
	specs := []struct {
		level, message string
		count          int
	}{
		{"error", "elasticsearch: query timed out after 2000ms", 9},
		{"warning", "index lag above 30s for products_v4", 4},
	}
	var traces []*trace
	sent := 0
	for _, spec := range specs {
		for _, at := range spreadTimes(now, 36*time.Hour, spec.count) {
			t := newTrace()
			root := t.add(nil, "search-api", "GET /search", kindServer, at, ms(jitter(2100)), map[string]string{"http.route": "/search", "http.response.status_code": "504"})
			es := t.add(root, "search-api", "POST elasticsearch/_search", kindClient, at.Add(ms(3)), ms(2000), map[string]string{"db.system.name": "elasticsearch"})
			if spec.level == "error" {
				es.fail(spec.message, "TimeoutError", "")
				root.errMessage = "upstream timeout"
			}
			traces = append(traces, t)
			a.sendError(key, map[string]any{
				"service": "search-api", "environment": "production", "level": spec.level, "message": spec.message,
				"timestamp": es.end.Format(time.RFC3339Nano), "attributes": map[string]string{"index": "products_v4"},
				"trace_id": t.id, "span_id": es.spanID,
			})
			sent++
		}
	}
	return sent, a.sendTraces(key, traces)
}

// spreadTimes returns count times within window before now, weighted toward
// recent ones, oldest first.
func spreadTimes(now time.Time, window time.Duration, count int) []time.Time {
	times := make([]time.Time, count)
	for i := range times {
		back := time.Duration(math.Pow(mrand.Float64(), 1.8) * float64(window))
		times[i] = now.Add(-back - time.Minute).Truncate(time.Millisecond)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times
}

func (a *api) sendError(key projectKey, event map[string]any) {
	body, _ := json.Marshal(event)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	hash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(key.secret))
	mac.Write([]byte(timestamp + "\nPOST\n/v1/errors\n" + hex.EncodeToString(hash[:])))
	request, _ := http.NewRequest(http.MethodPost, a.base+"/v1/errors", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Access-Key", key.access)
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	a.send(request, http.StatusAccepted)
}

// sendTraces posts spans as OTLP/JSON, 25 traces per request.
func (a *api) sendTraces(key projectKey, traces []*trace) int {
	sent := 0
	for start := 0; start < len(traces); start += 25 {
		end := min(start+25, len(traces))
		byService := map[string][]map[string]any{}
		for _, t := range traces[start:end] {
			for _, s := range t.spans {
				byService[s.service] = append(byService[s.service], s.otlp())
				sent++
			}
		}
		var resourceSpans []map[string]any
		for service, spans := range byService {
			resourceSpans = append(resourceSpans, map[string]any{
				"resource": map[string]any{"attributes": []map[string]any{
					kv("service.name", service), kv("deployment.environment.name", "production"), kv("service.version", "2.3.1"),
				}},
				"scopeSpans": []map[string]any{{"scope": map[string]any{"name": "seed"}, "spans": spans}},
			})
		}
		body, _ := json.Marshal(map[string]any{"resourceSpans": resourceSpans})
		request, _ := http.NewRequest(http.MethodPost, a.base+"/v1/traces", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Access-Key", key.access)
		request.Header.Set("X-Secret-Key", key.secret)
		a.send(request, http.StatusOK)
	}
	return sent
}

func (s *span) otlp() map[string]any {
	attrs := []map[string]any{}
	for k, v := range s.attrs {
		attrs = append(attrs, kv(k, v))
	}
	out := map[string]any{
		"traceId": s.traceID, "spanId": s.spanID, "name": s.name, "kind": s.kind,
		"startTimeUnixNano": strconv.FormatInt(s.start.UnixNano(), 10), "endTimeUnixNano": strconv.FormatInt(s.end.UnixNano(), 10),
		"attributes": attrs,
	}
	if s.parentID != "" {
		out["parentSpanId"] = s.parentID
	}
	if s.errMessage != "" {
		out["status"] = map[string]any{"code": 2, "message": s.errMessage}
	}
	var events []map[string]any
	for _, e := range s.events {
		eventAttrs := []map[string]any{}
		for k, v := range e.attrs {
			eventAttrs = append(eventAttrs, kv(k, v))
		}
		events = append(events, map[string]any{"name": e.name, "timeUnixNano": strconv.FormatInt(e.at.UnixNano(), 10), "attributes": eventAttrs})
	}
	if events != nil {
		out["events"] = events
	}
	return out
}

func kv(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func (a *api) send(request *http.Request, want int) {
	response, err := a.http.Do(request)
	if err != nil {
		fail("%s %s: %v", request.Method, request.URL.Path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		fail("%s %s returned %d: %s", request.Method, request.URL.Path, response.StatusCode, body)
	}
}

func (a *api) call(method, path string, body, out any) (int, string) {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, a.base+path, reader)
	request.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		request.Header.Set("Authorization", "Bearer "+a.token)
	}
	response, err := a.http.Do(request)
	if err != nil {
		fail("%s %s: %v (is the API running at %s?)", method, path, err, a.base)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	return response.StatusCode, strings.TrimSpace(string(data))
}

func (a *api) must(method, path string, body, out any, want int) {
	if status, data := a.call(method, path, body, out); status != want {
		fail("%s %s returned %d: %s", method, path, status, data)
	}
}

func randomHex(bytesCount int) string {
	b := make([]byte, bytesCount)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "seed: "+format+"\n", args...)
	os.Exit(1)
}

const goTimeoutStack = `context deadline exceeded
goroutine 214 [running]:
payments-api/internal/stripe.(*Client).Charge(0xc0001a2000, {0x10a3c80, 0xc000312000}, 0xc0002f4000)
	/app/internal/stripe/client.go:88 +0x1f4
payments-api/internal/service.(*ChargeService).Charge(0xc000118090, {0x10a3c80, 0xc000312000}, {0xc00028e0c0, 0x24}, 0x2710)
	/app/internal/service/charge.go:57 +0x2a6
payments-api/internal/http.(*Handler).createCharge(0xc0001180a0, {0x10a2f40, 0xc000300000}, 0xc00030e000)
	/app/internal/http/charge_handler.go:41 +0x1b8
net/http.HandlerFunc.ServeHTTP(0xc000120040, {0x10a2f40, 0xc000300000}, 0xc00030e000)
	/usr/local/go/src/net/http/server.go:2220 +0x29
net/http.(*ServeMux).ServeHTTP(0xc000110000, {0x10a2f40, 0xc000300000}, 0xc00030e000)
	/usr/local/go/src/net/http/server.go:2747 +0x1c4`

const goPanicStack = `panic: runtime error: index out of range [3] with length 3

goroutine 87 [running]:
inventory-api/internal/catalog.(*Repository).VariantBySize(...)
	/app/internal/catalog/repository.go:112
inventory-api/internal/catalog.(*Service).Product(0xc000126000, {0x10b2d80, 0xc0003a8000}, {0xc0002a6000, 0x8})
	/app/internal/catalog/service.go:64 +0x3c5
inventory-api/internal/http.(*Handler).getProduct(0xc000126010, {0x10b1e40, 0xc0003b2000}, 0xc0003a4000)
	/app/internal/http/products.go:33 +0x1a4
net/http.HandlerFunc.ServeHTTP(0xc000130020, {0x10b1e40, 0xc0003b2000}, 0xc0003a4000)
	/usr/local/go/src/net/http/server.go:2220 +0x29`

const jsStack = `TypeError: Cannot read properties of undefined (reading 'items')
    at calculateTotals (/app/src/cart/totals.js:18:32)
    at CheckoutService.createOrder (/app/src/checkout/service.js:44:19)
    at async /app/src/routes/checkout.js:27:21
    at async Layer.handle [as handle_request] (/app/node_modules/express/lib/router/layer.js:95:5)
    at async next (/app/node_modules/express/lib/router/route.js:149:13)`

const pythonStack = `Traceback (most recent call last):
  File "/app/worker/consumer.py", line 88, in run
    handler(message)
  File "/app/worker/handlers/order_created.py", line 42, in handle
    address = order["shipping_address"]
KeyError: 'shipping_address'`

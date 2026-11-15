package observability

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// StartTracing sends this process's OpenTelemetry spans to the platform, into
// the project of config's API key, and installs the W3C trace context
// propagator. Call the returned function on shutdown to flush buffered spans.
func StartTracing(ctx context.Context, config Config) (func(context.Context) error, error) {
	if config.AccessKey == "" || config.SecretKey == "" || strings.TrimSpace(config.Service) == "" {
		return nil, errors.New("observability: AccessKey, SecretKey and Service are required for tracing")
	}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(strings.TrimRight(config.Endpoint, "/")+"/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{"X-Access-Key": config.AccessKey, "X-Secret-Key": config.SecretKey}),
	)
	if err != nil {
		return nil, err
	}
	attributes := []attribute.KeyValue{attribute.String("service.name", config.Service)}
	if config.Environment != "" {
		attributes = append(attributes, attribute.String("deployment.environment.name", config.Environment))
	}
	res, err := resource.New(ctx, resource.WithAttributes(attributes...), resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return provider.Shutdown, nil
}

// InstrumentMux wraps mux so every request gets a SERVER span named after its
// route (for example "GET /orders/{id}"), continues traces from incoming
// traceparent headers, and reports panics as fatal events linked to the trace.
func (c *Client) InstrumentMux(mux *http.ServeMux) http.Handler {
	return otelhttp.NewHandler(c.Middleware(RouteNamer(mux)), "http.server", otelhttp.WithSpanNameFormatter(SpanNameFromRoute))
}

// SpanNameFromRoute is an otelhttp span name formatter: "METHOD /route" once
// the route is known, otherwise just the method. Route patterns keep span
// names low in cardinality ("GET /orders/{id}", not one name per order id).
func SpanNameFromRoute(_ string, r *http.Request) string {
	if route := routeOf(r.Pattern); route != "" {
		return r.Method + " " + route
	}
	return r.Method
}

type routeKey struct{}

type routeHolder struct{ pattern string }

// RouteNamer names the current span after the ServeMux pattern that serves
// the request and sets http.route. It works with nested muxes: the innermost
// (most specific) pattern wins, and the outermost RouteNamer copies it to
// r.Pattern so otelhttp's final span name uses it instead of a prefix like
// "/v1/accounts/".
func RouteNamer(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		holder, nested := r.Context().Value(routeKey{}).(*routeHolder)
		next := r
		if !nested {
			holder = &routeHolder{}
			next = r.WithContext(context.WithValue(r.Context(), routeKey{}, holder))
		}
		if _, pattern := mux.Handler(r); pattern != "" {
			holder.pattern = pattern
			span := trace.SpanFromContext(r.Context())
			span.SetName(r.Method + " " + routeOf(pattern))
			span.SetAttributes(attribute.String("http.route", routeOf(pattern)))
		}
		mux.ServeHTTP(w, next)
		if !nested && holder.pattern != "" {
			r.Pattern = holder.pattern
		}
	})
}

// routeOf strips the method and host from a ServeMux pattern.
func routeOf(pattern string) string {
	route := pattern
	if i := strings.IndexByte(route, ' '); i >= 0 {
		route = route[i+1:]
	}
	if i := strings.IndexByte(route, '/'); i > 0 {
		route = route[i:]
	}
	return route
}

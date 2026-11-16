// Package telemetry sets up OpenTelemetry tracing for the API itself.
package telemetry

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Setup enables tracing when OTEL_EXPORTER_OTLP_ENDPOINT (or
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) is set. The exporter reads the standard
// OTEL_* variables, so the API can send its spans to any OTLP backend,
// including this platform with OTEL_EXPORTER_OTLP_HEADERS set to a project's
// x-access-key and x-secret-key. Without those variables, tracing is off and
// every span is a no-op. The W3C trace context propagator is always installed
// so incoming trace headers are honoured.
func Setup(ctx context.Context, serviceName string) (shutdown func(context.Context) error, enabled bool, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	noop := func(context.Context) error { return nil }
	if os.Getenv("OTEL_SDK_DISABLED") == "true" ||
		(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "") {
		return noop, false, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, false, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", serviceName)),
		resource.WithFromEnv(), // OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES override the default
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return noop, false, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, true, nil
}

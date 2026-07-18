// Package telemetry initialises OpenTelemetry tracing. Export is enabled
// only when OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise a no-op provider
// keeps instrumentation costs at zero.
package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Init configures the global tracer provider and W3C propagation. The
// returned shutdown flushes buffered spans; call it after servers drain.
func Init(ctx context.Context, serviceName, version string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create otlp exporter: %w", err)
	}

	// The service attributes are deliberately schemaless. resource.Merge
	// REFUSES to merge two resources carrying different schema URLs, and
	// resource.Default() tracks whatever schema the SDK ships — so pinning our
	// own semconv schema here made Init fail outright ("conflicting Schema
	// URL") the moment tracing was enabled, and would break again on every SDK
	// bump. NewSchemaless contributes the attributes without a schema of its
	// own, so the merged resource simply inherits the SDK's.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

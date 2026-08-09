package telemetry_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/zkrebbekx/flexitype/internal/telemetry"
)

// restoreGlobals puts the OpenTelemetry globals back after a test, so one
// test's wiring cannot leak into the next.
//
// The tracer provider needs care. otel.GetTracerProvider() returns the global
// DELEGATING wrapper, not whatever is behind it, so capturing that value and
// setting it back is a no-op — the runtime says so: "Setting tracer provider
// to its current value. No delegate will be configured". Once one test
// installed an SDK provider as the delegate it was permanent for the process,
// and a second run of this package saw a recording span where it expected
// none.
//
// Installing an explicit no-op restores the observable behaviour instead: the
// delegate is replaced, and spans stop recording, which is the state a fresh
// process starts in.
func restoreGlobals(t *testing.T) {
	t.Helper()
	propagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTextMapPropagator(propagator)
		otel.SetTracerProvider(noop.NewTracerProvider())
	})
}

func TestInitWithoutEndpoint(t *testing.T) {
	Convey("Given no OTLP endpoint is configured", t, func() {
		restoreGlobals(t)
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

		before := otel.GetTracerProvider()

		Convey("When telemetry is initialised", func() {
			shutdown, err := telemetry.Init(context.Background(), "flexitype", "1.0.0")

			Convey("Then it succeeds and returns a usable no-op shutdown", func() {
				So(err, ShouldBeNil)
				So(shutdown, ShouldNotBeNil)
				So(shutdown(context.Background()), ShouldBeNil)

				// Idempotent: nothing to flush, so calling it again is fine.
				So(shutdown(context.Background()), ShouldBeNil)
			})

			Convey("Then W3C trace-context and baggage propagation is installed globally", func() {
				fields := otel.GetTextMapPropagator().Fields()
				So(fields, ShouldContain, "traceparent")
				So(fields, ShouldContain, "tracestate")
				So(fields, ShouldContain, "baggage")
			})

			Convey("Then no SDK tracer provider is installed — instrumentation stays free", func() {
				So(otel.GetTracerProvider(), ShouldEqual, before)
				_, isSDK := otel.GetTracerProvider().(*sdktrace.TracerProvider)
				So(isSDK, ShouldBeFalse)
			})

			Convey("Then spans started against the global provider do not record", func() {
				_, span := otel.Tracer("test").Start(context.Background(), "op")
				So(span.IsRecording(), ShouldBeFalse)
				span.End()
			})
		})
	})
}

// TestInitWithEndpoint is the regression test for a bug that made tracing
// impossible to enable at all.
//
// Init used to fail whenever OTEL_EXPORTER_OTLP_ENDPOINT was set — the only
// supported way to turn tracing on — with:
//
//	build otel resource: conflicting Schema URL:
//	  https://opentelemetry.io/schemas/1.41.0 and
//	  https://opentelemetry.io/schemas/1.26.0
//
// telemetry.go merged resource.Default() (whose schema URL tracks the SDK)
// with a resource pinned to semconv/v1.26.0, and resource.Merge rejects
// conflicting schema URLs — so the tracer provider was never installed and
// cmd/flexitype turned the error into a hard boot failure. The service
// refused to start the moment an operator enabled OTLP tracing.
//
// The fix builds the service attributes with resource.NewSchemaless, so the
// merged resource inherits the SDK's schema instead of fighting it — which
// also survives future SDK schema bumps.
func TestInitWithEndpoint(t *testing.T) {
	Convey("Given an OTLP endpoint is configured", t, func() {
		restoreGlobals(t)
		// Port 1 on loopback is never listening. The HTTP exporter connects
		// lazily and no span is ever exported here, so nothing is dialled.
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")

		Convey("When telemetry is initialised", func() {
			shutdown, err := telemetry.Init(context.Background(), "flexitype", "9.9.9")

			Convey("Then it succeeds and installs an SDK tracer provider", func() {
				So(err, ShouldBeNil)
				So(shutdown, ShouldNotBeNil)

				provider, isSDK := otel.GetTracerProvider().(*sdktrace.TracerProvider)
				So(isSDK, ShouldBeTrue)
				So(provider, ShouldNotBeNil)
			})

			Convey("Then spans started against the global provider record", func() {
				So(err, ShouldBeNil)
				_, span := otel.Tracer("test").Start(context.Background(), "op")
				So(span.IsRecording(), ShouldBeTrue)
				So(span.SpanContext().IsValid(), ShouldBeTrue)
				span.End()
			})

			Convey("Then the returned shutdown drains the provider within its deadline", func() {
				So(err, ShouldBeNil)
				So(shutdown, ShouldNotBeNil)

				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				So(shutdown(ctx), ShouldBeNil)

				// After shutdown the provider stops recording.
				_, span := otel.Tracer("test").Start(context.Background(), "after-shutdown")
				So(span.IsRecording(), ShouldBeFalse)
				span.End()
			})

			Convey("Then propagation is installed regardless of the exporter", func() {
				// This holds today: propagation is wired before the resource
				// is built, so it survives the failure above.
				So(otel.GetTextMapPropagator().Fields(), ShouldContain, "traceparent")
			})
		})
	})
}

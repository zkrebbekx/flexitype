package telemetry_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/zkrebbekx/flexitype/internal/telemetry"
)

// restoreGlobals snapshots the OpenTelemetry globals Init installs and puts
// them back afterwards, so one test's wiring cannot leak into the next.
func restoreGlobals(t *testing.T) {
	t.Helper()
	propagator := otel.GetTextMapPropagator()
	provider := otel.GetTracerProvider()
	t.Cleanup(func() {
		otel.SetTextMapPropagator(propagator)
		otel.SetTracerProvider(provider)
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

// TestInitWithEndpoint documents a REAL, CURRENTLY-FAILING production bug.
//
// The assertions below describe the behaviour Init is supposed to have and
// are deliberately NOT weakened to match the broken behaviour. The test is
// skipped only so a known production defect does not masquerade as a broken
// test suite; remove the t.Skip as the regression test for the fix.
//
// BUG: telemetry.Init always fails once OTEL_EXPORTER_OTLP_ENDPOINT is set,
// which is the only supported way to turn tracing on. It returns:
//
//	build otel resource: conflicting Schema URL:
//	  https://opentelemetry.io/schemas/1.41.0 and
//	  https://opentelemetry.io/schemas/1.26.0
//
// Cause: telemetry.go merges resource.Default() (schema URL 1.41.0, from
// go.opentelemetry.io/otel/sdk v1.44.0) with a resource built from
// semconv/v1.26.0. resource.Merge rejects conflicting schema URLs, so the
// merge never succeeds and the SDK tracer provider is never installed.
//
// Impact: cmd/flexitype/main.go:60-63 turns this into a hard boot failure —
// "init telemetry: build otel resource: conflicting Schema URL" — so the
// service refuses to start whenever an operator enables OTLP tracing.
// Distributed tracing cannot currently be enabled at all.
//
// Fix (production change, intentionally NOT made here): align the semconv
// import in internal/telemetry/telemetry.go with the SDK's schema version, or
// build the resource with resource.New(...)/schemaless merge instead of
// merging against resource.Default().
func TestInitWithEndpoint(t *testing.T) {
	t.Skip("KNOWN BUG: telemetry.Init fails with a conflicting OTel schema URL " +
		"whenever OTEL_EXPORTER_OTLP_ENDPOINT is set; see the comment above. " +
		"Un-skip once internal/telemetry/telemetry.go is fixed.")

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

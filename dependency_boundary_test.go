package flexitype_test

import (
	"os/exec"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestDependencyBoundaries pins what the core library is allowed to pull in.
//
// Two boundaries had been crossed, and both cost an adopter rather than us.
// The core module carried the whole Google Cloud Pub/Sub tree — 132 modules —
// for an optional publisher the facade never links, and with it a patch-exact
// `go 1.25.8` floor: a monorepo on a pinned Go 1.25.0-1.25.7 SDK could not
// build flexitype at all. Separately, infrastructure/postgres imported
// pkg/metrics purely to name an interface, so the storage adapter dragged in
// an HTTP router and a metrics library, inverting the layering this
// repository documents.
//
// These are cheap `go list` assertions rather than architecture rules, because
// what matters is the shipped module graph, not the import statements.
func TestDependencyBoundaries(t *testing.T) {
	Convey("Given the core library's package dependencies", t, func() {
		deps := goList(t, "-deps", "github.com/zkrebbekx/flexitype")

		Convey("Then it links no Google Cloud package", func() {
			So(matching(deps, "cloud.google.com"), ShouldBeEmpty)
		})

		Convey("Then it links no gRPC package", func() {
			So(matching(deps, "google.golang.org/grpc"), ShouldBeEmpty)
		})
	})

	Convey("Given the Postgres storage adapter's dependencies", t, func() {
		deps := goList(t, "-deps", "./infrastructure/postgres")

		Convey("Then it does not reach the HTTP router", func() {
			// pkg/metrics needs chi to resolve route patterns for its
			// middleware. The delivery-depth contract lives in
			// pkg/deliverystats so the adapter never has to import it.
			So(matching(deps, "github.com/go-chi"), ShouldBeEmpty)
		})

		Convey("Then it does not reach the metrics client", func() {
			So(matching(deps, "prometheus"), ShouldBeEmpty)
		})

		Convey("Then it still satisfies the delivery-stats contract", func() {
			So(matching(deps, "pkg/deliverystats"), ShouldNotBeEmpty)
		})
	})
}

// goList runs `go list` in the repository root and returns its lines. It uses
// GOWORK=off so the assertion reflects the published module, not the
// developer workspace, which unions every module in the repository.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Env = append(cmd.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// matching returns the lines containing substr.
func matching(lines []string, substr string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

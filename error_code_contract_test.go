package flexitype_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestErrorCodeContract holds the four published lists of error codes equal:
// the OpenAPI `Error.code` enum, the Go client's `ErrorCode` constants, the
// TypeScript client's `ERROR_CODES` array, and the table in
// docs/api-stability.md.
//
// They had drifted three ways at once. The client declared 9 codes where the
// server emits 12, omitting FEATURE_DISABLED, CURSOR_CONFLICT and
// CURSOR_EXPIRED — the last two being the two recovery branches every feed
// consumer must implement, so an adopter had to string-compare literals with
// no compile-time protection. The stability document listed 10, omitting
// RATE_LIMITED and INTERNAL. Nothing failed when they diverged, because each
// list lives in a different file and no test read more than one.
//
// The client is a separate module, so this reads its source rather than
// importing it. That is the point: the test belongs to the repository that
// publishes all three lists.
func TestErrorCodeContract(t *testing.T) {
	Convey("Given the four published lists of error codes", t, func() {
		spec := openAPIErrorCodes(t)
		client := clientErrorCodes(t)
		tsClient := tsClientErrorCodes(t)
		doc := stabilityDocErrorCodes(t)

		Convey("Then the spec enum is not empty, so the parse is meaningful", func() {
			So(len(spec), ShouldBeGreaterThan, 5)
		})

		Convey("Then the Go client declares exactly the codes the spec enumerates", func() {
			So(client, ShouldResemble, spec)
		})

		Convey("Then the TypeScript client declares exactly the same codes", func() {
			So(tsClient, ShouldResemble, spec)
		})

		Convey("Then the stability document lists exactly the same codes", func() {
			So(doc, ShouldResemble, spec)
		})
	})
}

// openAPIErrorCodes reads the Error schema's code enum from the spec.
func openAPIErrorCodes(t *testing.T) []string {
	t.Helper()
	body := readRepoFile(t, "api/openapi.yaml")
	m := regexp.MustCompile(`(?m)^\s*enum: \[(VALIDATION[^\]]*)\]`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("api/openapi.yaml: could not find the Error.code enum")
	}
	return sorted(strings.Split(m[1], ","))
}

// clientErrorCodes reads the ErrorCode constants from the client module's
// source. The client is a separate module, so this cannot be an import.
func clientErrorCodes(t *testing.T) []string {
	t.Helper()
	body := readRepoFile(t, "client/errors.go")
	var out []string
	for _, m := range regexp.MustCompile(`ErrorCode = "([A-Z_]+)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("client/errors.go: found no ErrorCode constants")
	}
	return sorted(out)
}

// tsClientErrorCodes reads the ERROR_CODES array from the TypeScript client's
// source. That package has its own vitest check of the same equality; this one
// exists so a Go-only contributor who adds a code to the service cannot leave
// the TypeScript client behind without CI saying so.
func tsClientErrorCodes(t *testing.T) []string {
	t.Helper()
	body := readRepoFile(t, "client-ts/src/errors.ts")
	const marker = "export const ERROR_CODES = ["
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("client-ts/src/errors.ts: could not find %q", marker)
	}
	end := strings.Index(body[start:], "] as const")
	if end < 0 {
		t.Fatal("client-ts/src/errors.ts: could not find the end of ERROR_CODES")
	}
	var out []string
	for _, m := range regexp.MustCompile(`'([A-Z_]+)'`).FindAllStringSubmatch(body[start:start+end], -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("client-ts/src/errors.ts: found no codes in ERROR_CODES")
	}
	return sorted(out)
}

// stabilityDocErrorCodes reads the backtick-quoted codes from the one
// paragraph in docs/api-stability.md that lists them.
func stabilityDocErrorCodes(t *testing.T) []string {
	t.Helper()
	body := readRepoFile(t, "docs/api-stability.md")
	const marker = "Error responses carry stable machine codes"
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("docs/api-stability.md: could not find %q", marker)
	}
	// The list ends at the sentence that follows it.
	end := strings.Index(body[start:], "New codes may be added")
	if end < 0 {
		t.Fatal("docs/api-stability.md: could not find the end of the code list")
	}
	var out []string
	for _, m := range regexp.MustCompile("`([A-Z_]+)`").FindAllStringSubmatch(body[start:start+end], -1) {
		out = append(out, m[1])
	}
	return sorted(out)
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// sorted trims, drops empties and deduplicates, so the three lists compare on
// content rather than on formatting.
func sorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

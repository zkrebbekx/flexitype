package flexitype_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// firstPartyReplace matches a replace of one of this repository's own modules.
var firstPartyReplace = regexp.MustCompile(`(?m)^\s*replace\s+github\.com/zkrebbekx/flexitype`)

// releaseTagged lists the nested modules .github/workflows/release.yml tags on
// a version tag. Keep it in step with the loop there.
var releaseTagged = []string{"client"}

// nestedModules is every module in the repository other than the root.
var nestedModules = []string{"client", "cmd/flexitype", "conformance", "infrastructure/gcppubsub"}

// TestReleaseTagsOnlyResolvableModules pins the invariant that decides which
// modules the release workflow may tag.
//
// A published module's `replace` is IGNORED by consumers. So a module that
// replaces a first-party dependency resolves only from a checkout, and tagging
// it produces a version `go get` cannot use — worse than no tag, because the
// module looks available. cmd/flexitype and infrastructure/gcppubsub are in
// that state today: they require the zero pseudo-version of the core module.
//
// docs/api-stability.md carries the two-release sequence that makes them
// go-gettable. When a module is fixed, it joins releaseTagged here and the
// workflow's loop, together.
func TestReleaseTagsOnlyResolvableModules(t *testing.T) {
	Convey("Given the repository's nested modules", t, func() {
		replaced := map[string]bool{}
		for _, m := range nestedModules {
			raw, err := os.ReadFile(filepath.Join(m, "go.mod"))
			So(err, ShouldBeNil)
			replaced[m] = firstPartyReplace.Match(raw)
		}

		Convey("Then the ones the release workflow tags carry no first-party replace", func() {
			for _, m := range releaseTagged {
				// Named in the assertion so a failure says WHICH module.
				So(map[string]bool{m: replaced[m]}, ShouldResemble, map[string]bool{m: false})
			}
		})

		Convey("Then a module that does carry one is NOT tagged", func() {
			for _, m := range nestedModules {
				if replaced[m] {
					So(releaseTagged, ShouldNotContain, m)
				}
			}
		})

		Convey("Then the workflow's own loop matches this list", func() {
			raw, err := os.ReadFile(filepath.Join(".github", "workflows", "release.yml"))
			So(err, ShouldBeNil)
			So(string(raw), ShouldContainSubstring, "for module in "+strings.Join(releaseTagged, " ")+"; do")
		})
	})
}

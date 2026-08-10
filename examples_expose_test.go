package flexitype_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// publishedPort matches a compose port mapping, e.g. `- "8080:8080"` or
// `- "127.0.0.1:8080:8080"`. The captured group is everything before the
// container port.
var publishedPort = regexp.MustCompile(`^\s*-\s*"([^"]+)"\s*$`)

// TestExamplesPublishOnLoopbackOnly is the guard for #603.3.
//
// Every example ships either with authentication off or with a demo credential
// printed in its compose file. The kitchen example set FLEXITYPE_DEV_INSECURE
// and published 8080 on every interface, so anybody who could reach the host
// reached the whole anonymous multi-tenant API. The others published a
// well-known demo token the same way.
//
// A host part is what stops that, and it is one character away from being
// dropped in an edit — so it is asserted rather than remembered.
func TestExamplesPublishOnLoopbackOnly(t *testing.T) {
	Convey("Given every example's compose file", t, func() {
		files, err := filepath.Glob("examples/*/docker-compose*.yml")
		So(err, ShouldBeNil)
		So(files, ShouldNotBeEmpty)

		Convey("When each published port is read", func() {
			offenders := []string{}
			checked := 0
			for _, file := range files {
				body, rerr := os.ReadFile(file)
				So(rerr, ShouldBeNil)
				inPorts := false
				for _, line := range strings.Split(string(body), "\n") {
					trimmed := strings.TrimSpace(line)
					if trimmed == "ports:" {
						inPorts = true
						continue
					}
					if !inPorts {
						continue
					}
					match := publishedPort.FindStringSubmatch(line)
					if match == nil {
						// The list ended: anything that is not a quoted
						// mapping closes it.
						inPorts = strings.HasPrefix(trimmed, "#")
						continue
					}
					checked++
					if !strings.HasPrefix(match[1], "127.0.0.1:") {
						offenders = append(offenders, file+": "+match[1])
					}
				}
			}

			Convey("Then none is reachable from off the host", func() {
				So(checked, ShouldBeGreaterThan, 0)
				So(offenders, ShouldBeEmpty)
			})
		})
	})
}

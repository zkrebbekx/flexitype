// The merchant-facing half of examples/marketplace. It is its own module so
// the example depends on the published client SDK exactly as an adopter's
// service would, and never on flexitype's internals.
//
// The replace directives point at this checkout, so the example always builds
// against the code beside it. A published module's replace is ignored by
// consumers, so this module is never tagged or `go get`-able — see
// release_modules_test.go.
module github.com/zkrebbekx/flexitype/examples/marketplace/platform

go 1.25.8

require (
	github.com/lib/pq v1.12.3
	github.com/zkrebbekx/flexitype/client v0.0.0
)

replace github.com/zkrebbekx/flexitype => ../../..

replace github.com/zkrebbekx/flexitype/client => ../../../client

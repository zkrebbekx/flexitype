package main

import (
	"net/http/httptest"
	"testing"

	"github.com/zkrebbekx/flexitype"
)

// newFlexitype boots an in-memory flexitype and returns its base URL.
//
// The example's whole claim is that the SERVICE does the costing, so the tests
// drive a real one. An in-memory service is enough: the computed materializer,
// the unit families and the change sets are the same code a Postgres-backed
// service runs.
func newFlexitype(t *testing.T) string {
	t.Helper()
	svc := flexitype.NewInMemory()
	srv := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{AllowAnonymous: true}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Package api embeds and serves the OpenAPI 3 description of the flexitype
// REST API. Point client generators, mock servers or Swagger UI at the
// served /api/v1/openapi.json.
package api

import (
	_ "embed"
	"sync"

	"sigs.k8s.io/yaml"
)

// SpecYAML is the raw OpenAPI document.
//
//go:embed openapi.yaml
var SpecYAML []byte

// EventEnvelopeSchema and EventPayloadSchemas are the machine-readable event
// contracts.
//
// The envelope was versioned and described in prose, and the payloads existed
// only as Go structs — so a consumer in another language transcribed them by
// hand and got no signal when one changed. These are embedded so a deployment
// can serve them, and TestEventSchemasMatchPayloads validates real emitted
// payloads against them, so a Go-side change that is not reflected here fails
// the build rather than a consumer's parser.
//
//go:embed schemas/envelope.schema.json
var EventEnvelopeSchema []byte

// EventPayloadSchemas is the payload half of the event contract: one
// definition per event family, keyed under $defs.
//
//go:embed schemas/payloads.schema.json
var EventPayloadSchemas []byte

var (
	jsonOnce sync.Once
	jsonSpec []byte
	jsonErr  error
)

// SpecJSON returns the OpenAPI document as JSON (converted once from the
// embedded YAML). The conversion is deterministic, so it is cached.
func SpecJSON() ([]byte, error) {
	jsonOnce.Do(func() {
		jsonSpec, jsonErr = yaml.YAMLToJSON(SpecYAML)
	})
	return jsonSpec, jsonErr
}

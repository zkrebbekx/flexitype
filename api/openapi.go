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

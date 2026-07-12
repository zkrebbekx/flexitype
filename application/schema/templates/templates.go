// Package templates ships curated starter schema bundles, embedded in the
// binary, that a tenant can apply in one call to bootstrap a working schema.
// Each template reuses the portable schema-bundle format (see application/
// schema), so a template is just a named, in-repo bundle.
package templates

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"

	appschema "github.com/zkrebbekx/flexitype/application/schema"
)

//go:embed *.json
var files embed.FS

// Template is a curated starter schema plus its human-facing metadata.
type Template struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Bundle      *appschema.Bundle `json:"bundle"`
}

// Summary is a template's metadata without its bundle body, for listings.
type Summary struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

var registry = mustLoad()

// mustLoad parses every embedded template at startup; a malformed template is
// a programming error (the content is curated and in-repo), so it panics.
func mustLoad() map[string]Template {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(fmt.Sprintf("templates: read embedded dir: %v", err))
	}
	out := make(map[string]Template, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := files.ReadFile(e.Name())
		if err != nil {
			panic(fmt.Sprintf("templates: read %s: %v", e.Name(), err))
		}
		var t Template
		if err := json.Unmarshal(raw, &t); err != nil {
			panic(fmt.Sprintf("templates: parse %s: %v", e.Name(), err))
		}
		if t.Name == "" || t.Bundle == nil {
			panic(fmt.Sprintf("templates: %s missing name or bundle", e.Name()))
		}
		if _, dup := out[t.Name]; dup {
			panic(fmt.Sprintf("templates: duplicate template name %q", t.Name))
		}
		out[t.Name] = t
	}
	return out
}

// List returns every template's metadata, sorted by name.
func List() []Summary {
	out := make([]Summary, 0, len(registry))
	for _, t := range registry {
		out = append(out, Summary{Name: t.Name, Title: t.Title, Description: t.Description})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Get returns one template by name.
func Get(name string) (Template, bool) {
	t, ok := registry[name]
	return t, ok
}

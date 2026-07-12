# API clients

## Go: the first-party client

For Go services, use the hand-crafted client at
[`github.com/zkrebbekx/flexitype/client`](../client) — a lightweight
(standard-library-only) module that mirrors the embedded usecase surface over
the REST API, so remote code reads much like embedded code.

```bash
go get github.com/zkrebbekx/flexitype/client
```

```go
import "github.com/zkrebbekx/flexitype/client"

c, err := client.New("https://flexitype.internal", client.WithToken(os.Getenv("FLEXITYPE_TOKEN")))

prod, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "product", DisplayName: "Product"})
price, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
    TypeDefinitionID: prod.ID, InternalName: "price", DisplayName: "Price", DataType: "decimal",
})
_, err = c.Values().Set(ctx, client.SetValueInput{
    AttributeDefinitionID: price.ID, EntityID: "sku-9", TypeDefinitionID: prod.ID,
    Value: json.RawMessage(`"19.99"`),
})

// FQL query as a keyset-paginated iterator (Go 1.23 range-over-func):
for row, err := range c.Query(ctx, "product", `price > 100`) {
    if err != nil { return err }
    fmt.Println(row.EntityID)
}

// Typed errors with sentinels:
if _, err := c.Types().Get(ctx, id); errors.Is(err, client.ErrNotFound) {
    // ...
}
```

Highlights:

- **Resource-grouped, mirrors the embedded API**: `c.Types()`, `c.Attributes()`,
  `c.Values()`, `c.Entities()`, `c.Relationships()`, `c.Dependencies()`,
  `c.UnitFamilies()`, `c.SavedViews()`, `c.ChangeSets()`, `c.Revisions()`,
  `c.Schema()`, `c.Webhooks()`, `c.Events()`, `c.Admin()`, plus `c.Query`,
  `c.GraphQL`, `c.Features`, `c.Reindex`.
- **Keyset pagination built in**: every list has a single-page `List(...)` and an
  auto-paginating `All(...)` that yields `iter.Seq2[T, error]`; request the total
  with `ListOptions{Total: true}`.
- **Typed errors**: `*client.APIError` with `errors.Is` sentinels
  (`ErrNotFound`, `ErrConflict`, `ErrValidation`, `ErrForbidden`, …).
- **Conformance-tested**: a CI test drives this client against the real handler
  in-process, so it can never drift from the API.

The client depends only on the standard library.

## Other languages: generate from OpenAPI

flexitype publishes its REST contract as an OpenAPI 3 document. The
running service serves it (unauthenticated) at:

- `GET /api/v1/openapi.json`
- `GET /api/v1/openapi.yaml`

and the same document is committed at [`api/openapi.yaml`](../api/openapi.yaml).
It is validated on every build (`go test ./api/...`), so it never drifts
into an invalid state.

## Generate a client

Point any OpenAPI generator at the served document or the committed file.
Using [openapi-generator](https://openapi-generator.tech):

```bash
# TypeScript (fetch)
npx @openapitools/openapi-generator-cli generate \
  -i http://localhost:8080/api/v1/openapi.json \
  -g typescript-fetch \
  -o ./flexitype-client-ts

# Go
npx @openapitools/openapi-generator-cli generate \
  -i api/openapi.yaml \
  -g go \
  -o ./flexitype-client-go
```

Other useful targets: `python`, `java`, `rust`, `csharp`, and
`html2`/`markdown` for browsable docs.

## Browse the API

Serve interactive docs with any spec viewer, e.g.:

```bash
npx @redocly/cli preview-docs api/openapi.yaml
# or point Swagger UI at http://localhost:8080/api/v1/openapi.json
```

## Authentication

All endpoints except the OpenAPI documents and the operational endpoints
(`/healthz`, `/readyz`, `/metrics`) require a service-account bearer token
(`Authorization: Bearer ft_<account>_<secret>`). In development mode (no
service-account file configured) auth is disabled. Configure the
generated client's bearer token accordingly.

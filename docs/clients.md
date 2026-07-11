# Generating API clients

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

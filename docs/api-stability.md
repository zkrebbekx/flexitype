# API stability & versioning

flexitype versions three surfaces: the **REST API** (`/api/v1`), the
**embedded Go API** (the `flexitype` package and its dependencies), and
the **storage schema** (migrations). This document states what stability
each offers and how changes are communicated.

## Release versioning

Releases follow [Semantic Versioning](https://semver.org/). From **1.0**, the
usual guarantees take effect: breaking changes only in majors, additive changes
in minors, fixes in patches. Every release is a git tag (`vX.Y.Z`), a GitHub
release, and an entry in [CHANGELOG.md](../CHANGELOG.md). Pin a tag; do not
depend on `main`.

## REST API (`/api/v1`)

- The `v1` path prefix is the compatibility boundary. Within `v1`, changes
  are **additive** after 1.0: new endpoints, new optional request fields,
  new response fields. Clients must ignore unknown response fields.
- Breaking changes to request or response shapes ship under a new prefix
  (`/api/v2`); `v1` is then supported for at least one minor release cycle
  with a deprecation notice in the changelog and a `Deprecation` response
  header.
- Error responses carry stable machine codes (`VALIDATION`, `NOT_FOUND`,
  `CONFLICT`, `ARCHIVED`, `DEPENDENCY_VIOLATION`, `FEATURE_DISABLED`,
  `CURSOR_CONFLICT`, `CURSOR_EXPIRED`, `UNAUTHENTICATED`, `FORBIDDEN`).
  New codes may be added; existing codes keep their meaning.
- The event envelope has a `schema_version` field; a bump signals a
  breaking payload change and is called out in the changelog.

## Embedded Go API

Not every exported symbol in the module is a supported API. The **supported
surface** — the only symbols that carry the SemVer compatibility promise from
1.0 — is:

- **The facade**: the root `github.com/zkrebbekx/flexitype` package —
  `New`/`NewInMemory`, its `Option`s, `Service` and the types they exchange.
- **The Go client module**: `github.com/zkrebbekx/flexitype/client` (see
  below; separately versioned).
- **Extension ports** you implement and hand to the facade:
  - `pkg/events`: `Handler`, `HandlerFunc`, `Publisher`, `TopicFunc`,
    `WebhookConfig` (registered via `WithHandler`/`WithPublisher`/`WithWebhook`).
  - `pkg/blob`: `Store` (via `WithBlobStore`).
  - `pkg/serviceaccount`: `Authenticator`/`AuthenticatorCtx`, `Account`, `Scope`
    (the auth boundary for the standalone server).
  - `pkg/db`: `Transactor` (the pool handle passed to `New`).

Everything else is **internal, with no compatibility promise**, even though Go
requires it to be exported for the facade to wire it together — treat it as you
would `internal/`. This includes: the `application/*` interactors, their input
structs and stores; the `domain/*` aggregates and repository ports; both
storage backends (`infrastructure/*`); the FQL and formula ASTs (`pkg/fql`,
`pkg/formula`); and deployment plumbing. Do **not** import these directly from
outside the module; configure behaviour through the facade's `Option`s instead.
They may change in any release.

- `internal/` is never part of the public API. Deployment plumbing that only
  `cmd/flexitype` and the facade use (`config`, `shutdown`, `telemetry`,
  `safedial`) lives under `internal/` precisely so it cannot be imported as if
  it were supported.

## Go client module

- The first-party client, `github.com/zkrebbekx/flexitype/client`, is a
  **separate Go module** and is versioned by its own `client/vX.Y.Z` tags,
  which the release workflow cuts in lockstep with the main `vX.Y.Z` tag. So
  `go get github.com/zkrebbekx/flexitype/client@vX.Y.Z` resolves to the client
  as it shipped in that release.
- Its exported surface follows SemVer from 1.0, tracking the REST API's
  compatibility guarantees. It depends only on the standard library.

## Storage schema

- Migrations are forward-only and idempotent; applying a newer binary to
  an older database is always safe. Downgrading the binary is not
  supported across a migration that changed data shape.
- A migration that changes an existing column or table in a
  backwards-incompatible way only ships in a minor (pre-1.0) or major
  (post-1.0) release and is flagged in the changelog.

## Reporting

Security issues: see [SECURITY.md](../SECURITY.md). Everything else:
GitHub issues.

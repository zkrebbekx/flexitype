# API stability & versioning

flexitype versions three surfaces: the **REST API** (`/api/v1`), the
**embedded Go API** (the `flexitype` package and its dependencies), and
the **storage schema** (migrations). This document states what stability
each offers and how changes are communicated.

## Release versioning

Releases follow [Semantic Versioning](https://semver.org/). From **1.0**, the
usual guarantees take effect: breaking changes only in majors, additive changes
in minors, fixes in patches.

**One carve-out, stated because it has already been used.** A fix that makes
the API behave as documented can ship in a minor even when it rejects a
request that previously succeeded — a validation gap closed, or a wrong status
code corrected. v1.2.0 used it four times: quoted values for integer
attributes are refused, `DELETE` of a missing unit family or match rule
answers 404 rather than 204, provisioning routes authorize before the feature
gate, and a bad `?limit=`/`?cursor=` on `/activity` answers 422 rather than
500. v1.3.0 used it twice: a formula that reads or folds a non-numeric
attribute is refused with 422 rather than materializing `0`, and a purge that
cannot remove the rows still matching its predicate reports an error rather
than a success receipt. The carve-out also covers every paginated list: a
`?cursor=` whose value the ordering column cannot parse, or that carries the
wrong number of values, answers 422 rather than 500 or a silent restart at
page 1. Each such change is listed in the changelog under a
**BREAKING** heading with the old and new behaviour. What does *not* ship in a minor is a change to
a shape or a name that behaved correctly. Every release is a git tag (`vX.Y.Z`), a GitHub
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
  `CURSOR_CONFLICT`, `CURSOR_EXPIRED`, `UNAUTHENTICATED`, `FORBIDDEN`,
  `RATE_LIMITED`, `INTERNAL`). New codes may be added; existing codes keep
  their meaning. This list, the `Error.code` enum in `api/openapi.yaml` and
  the `ErrorCode` constants in the Go client are held equal by a test.
- The event envelope has a `schema_version` field; a bump signals a
  breaking payload change and is called out in the changelog.

## Embedded Go API

Not every exported symbol in the module is a supported API. The **supported
surface** — the only symbols that carry the SemVer compatibility promise from
1.0 — is:

- **The facade**: the root `github.com/zkrebbekx/flexitype` package —
  `New`/`NewInMemory`, its `Option`s, `Service` and the types they exchange.
- **The data surface**: the interactors reached through
  `Service.Interactors(ctx)` and `Service.Factory()` — their method
  signatures, their `…Input` structs, and the types they return.

  This was previously excluded, which made the promise hollow: no facade
  method performs a CRUD operation, so every line of an embedder's business
  logic went through a layer the document disclaimed. A team could evaluate
  library mode on the strength of this page and find there was no version to
  pin that gave them the guarantee they had read.

  What stays internal inside those packages: the repository and store ports
  they depend on, their constructors, and everything unexported. Take the
  interactors from the facade; do not build one yourself.
- **The Go client module**: `github.com/zkrebbekx/flexitype/client` (see
  below; separately versioned).
- **Extension ports** you implement and hand to the facade:
  - `pkg/events`: `Handler`, `HandlerFunc`, `Publisher`, `TopicFunc`,
    `WebhookConfig` (registered via `WithHandler`/`WithPublisher`/`WithWebhook`).
  - `pkg/blob`: `Store` (via `WithBlobStore`).
  - `pkg/serviceaccount`: `Authenticator`/`AuthenticatorCtx`, `Account`, `Scope`
    (the auth boundary for the standalone server).

`pkg/db` is **not** an extension port. It was listed as one through 1.0, in
error: no facade option accepts a `Transactor`. `New(pool *sqlx.DB, opts
...Option)` takes a concrete `*sqlx.DB` and builds the transactor itself, so
**`github.com/jmoiron/sqlx` is part of the supported signature** — that is a
dependency an embedder inherits, and it is named here rather than discovered
late by someone designing around "we supply our own transaction handle". `db.Transactor`, `db.Tx` and
`db.TxMarker` are internal wiring and change without notice — `Tx` was
resealed as an opaque marker in 1.1.0. Supply your own pool to `New` to
control connection settings; there is no supported way to substitute a
transaction manager.

Everything else is **internal, with no compatibility promise**, even though Go
requires it to be exported for the facade to wire it together — treat it as you
would `internal/`. This includes: the stores and repository ports the
`application/*` interactors depend on, and the interactors' own constructors;
the `domain/*` aggregates and repository ports; both storage backends
(`infrastructure/*`); the FQL and formula ASTs (`pkg/fql`, `pkg/formula`); and
deployment plumbing. Do **not** construct these directly; reach the interactors
through `Service.Interactors(ctx)` and configure behaviour through the facade's
`Option`s. They may change in any release.

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

## Module layout

The repository holds five Go modules. The split exists so that the library a
team embeds carries only what the library needs:

| Module | Path | Purpose |
|---|---|---|
| Core library | `github.com/zkrebbekx/flexitype` | The embedded API. This is what an adopter imports. |
| Go client | `.../client` | REST client. Zero dependencies. |
| Standalone server | `.../cmd/flexitype` | The binary. Built from source or taken as an image. |
| Pub/Sub adapter | `.../infrastructure/gcppubsub` | Optional Google Cloud publisher. |
| Conformance suite | `conformance` | Tests only; not published. |

The server and the Pub/Sub adapter are separate modules because the Pub/Sub
client pulls 132 modules (`cloud.google.com`, `google.golang.org/{api,grpc,
genproto}`, `googleapis`, `s2a-go`, `opencensus`) and declared a **patch-exact
`go 1.25.8`** floor. While they lived in the core module, that floor applied
to every embedder: a monorepo pinned to a Go 1.25.0–1.25.7 SDK — and
`rules_go` effectively runs `GOTOOLCHAIN=local` — could not build flexitype at
all, for an optional publisher it never linked. The core module now requires
124 modules rather than 271, needs `go 1.25`, and links no
`cloud.google.com` package.

The server and adapter modules carry `replace` directives to the repository
root, so they build from a checkout but are **not `go get`-able**. Build them
from source or take the container image.

`go.work` wires all five together for local development. It is not a
substitute for the `replace`: both modules require the core at the zero
pseudo-version, and the module graph is loaded before workspace substitution
applies, so removing the `replace` breaks the build inside the workspace too.
(Measured, not assumed — `go build` then fails with
`invalid version: unknown revision 000000000000`.)

**The release workflow does not tag them.** A published module's `replace` is
ignored by consumers, so tagging one produces a version `go get` cannot
resolve — worse than no tag, because the module looks available. Only
`client`, which has no first-party `replace`, is tagged. A test pins that
correspondence (`TestReleaseTagsOnlyResolvableModules`).

### Making them go-gettable

It takes two releases, because `cmd/flexitype` requires
`infrastructure/gcppubsub`, which has never been published — there is no real
version to name until one exists.

1. **Release N.** Cut the core tag as normal. The core module's zip already
   excludes `cmd/flexitype/`, `infrastructure/gcppubsub/` and `client/`,
   because each contains a `go.mod` — verified by building the zip with
   `golang.org/x/mod/zip`, the library the toolchain uses: **0 files** from
   each of those directories. So the "same package path in two modules"
   hazard is already gone.
2. **After the proxy has release N**, in one commit:
   - `infrastructure/gcppubsub/go.mod`: require the core at `vN`, drop the
     `replace`.
   - `cmd/flexitype/go.mod`: require the core and the adapter at `vN`, drop
     both `replace` directives.
   - Add each module to the release workflow's tag loop and to
     `releaseTagged` in the test above — they change together by design.
3. **Release N+1** tags all four modules. From then on
   `go get github.com/zkrebbekx/flexitype/cmd/flexitype@vN+1` resolves.

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

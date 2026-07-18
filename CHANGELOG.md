# Changelog

All notable changes to flexitype are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0
(see [API stability](docs/api-stability.md)).

## [Unreleased]

## [1.1.0] — 2026-07-18

A post-1.0 independent review (security, architecture, performance and
coding-standards) plus follow-ups — every issue implemented and merged. The
REST API (`/api/v1`), the storage schema (forward-only migrations), and the
supported Go facade stay backward-compatible; changes to them are additive. The
only breaking changes are to unsupported Go internals — see below.

### Security

- **Media download is tenant-scoped** — an object key is served only to the
  tenant that owns it; a mismatch is a `404`, so ownership is not probeable
  (was a cross-tenant IDOR).
- **Field-level ACL now covers relationship (link) attributes** in the FQL
  binder, closing a binary-search value oracle on restricted link attributes.
- **CSV export neutralises formula injection** — a cell starting with `=`, `+`,
  `-`, `@`, tab or CR is quoted so spreadsheets treat it as text (CWE-1236).
- **Response hardening** — media downloads force `Content-Disposition:
  attachment` + `nosniff` + a content-free CSP; a middleware sets a restrictive
  Content-Security-Policy (script-src pinned to `'self'` + the console's
  hashed inline theme script, no `'unsafe-inline'`), `X-Frame-Options: DENY`,
  `nosniff` and `Referrer-Policy: no-referrer` on every response.
- **Bootstrap admin hardened** — the existence check fails closed (a transient
  error no longer mints a fresh credential) and the token is printed to stdout
  once, never through the structured logger. The `admin` scope is documented as
  a global platform-operator privilege.
- GraphQL queries have a field-count and execution-time budget; request bodies
  are capped.

### Added

- `POST /api/v1/computed/recompute` and `Service.RecomputeComputed` — rebuild a
  tenant's computed attributes (the recovery counterpart to `search/reindex`).
- **Quantity data type in the admin console** — a magnitude + unit editor and
  `{magnitude} {unit}` rendering; the `DataType` union now covers `quantity`.
- `WithCleanupObserver` (surface swallowed post-erasure cleanup failures) and a
  context-aware `AuthenticatorCtx` extension point (the credential lookup now
  honours the request's cancellation/deadline/trace).
- The first-party Go client defaults to a 30s per-request HTTP timeout.

### Performance

- **Entity-summary projection** (trigger-maintained) turns entity-list and the
  FQL enumeration base into a bounded keyset index scan instead of
  re-aggregating every value row per page (~313 ms → ~0.3 ms at 200k entities;
  constant rather than linear in entity count).
- **Replica-safe GraphQL schema cache** — a persisted per-tenant
  `schema_version` (trigger-maintained) drives invalidation across replicas,
  with a short memo and an LRU bound.
- **Windowed GraphQL nested connections** — each parent fetches only `first+1`
  children via a `row_number()` window with the definition filter pushed into
  SQL, routed through a dataloader (no N+1, no full materialisation).
- Coalesced per-commit search/computed projection maintenance; chunked CSV
  import; a shared-trigram inverted index for duplicate detection; supporting
  indexes for webhook delivery, decimal comparisons and attribute-value scans.
- A `//go:build stress` harness seeds up to 10M entities and profiles CRUD /
  FQL / GraphQL.

### Changed

- **Internal projections (computed attributes, search index, GraphQL schema
  cache) are maintained in the originating unit of work's post-commit, in both
  delivery modes** — so a write's own computed values and `matches()` results
  are visible to that request (read-your-writes) independent of `WithOutbox`.
  The external event dispatcher is reserved for consumer hooks. A projection
  failure is surfaced to `WithDispatchObserver`, never silently swallowed;
  recover post-crash staleness with `search/reindex` / `computed/recompute`.
- **Erasure is atomic and honest** — the revision purge joins the value
  transaction, projection removal uses one consistent post-commit policy, and
  the `PurgeReport` counts only confirmed blob deletions (new `MediaBlobsFailed`
  / `UnpurgedBlobKeys`).
- Usecase timestamps are normalised to UTC in one place.

### Fixed

- In-memory backend transaction isolation — a per-transaction undo journal
  replaces the whole-store snapshot, so interleaved transactions no longer
  clobber each other's committed writes, and a write is O(touched keys).

### Breaking — Go library embedders only

No impact on REST/CLI/Docker consumers, the storage schema, or the supported Go
facade. The supported public Go surface is now explicitly the `flexitype`
facade, the `client` module, and the documented extension ports
(`events.Handler`/`Publisher`, `blob.Store`, `serviceaccount.Authenticator`,
`db.Transactor`); everything else is internal, with no compatibility promise
(see [API stability](docs/api-stability.md)).

- Deployment plumbing moved from `pkg/` to `internal/`: `config`, `shutdown`,
  `telemetry`, `safedial`.
- `application/*` and `domain/*` internals were restructured: an `appctx` leaf
  package breaks the application-root dependency cycle; an `erasure.Interactor`
  owns the purge flow and the value interactor's setter injection is replaced
  by constructor config; the domain repository ports were slimmed and the SQL
  executor removed from domain signatures (an opaque `db.Tx` marker).

### Internal

- Full FQL parity corpus and PostgreSQL behavioural parity test coverage across
  the previously memory-only suites; a completeness guard fails CI if a new FQL
  construct is left uncovered.

## [1.0.0] — 2026-07-13

First stable release. The full feature set is verified against PostgreSQL 16
and covered by the test suite (both the Postgres and in-memory backends, with a
cross-backend FQL parity corpus). SemVer applies from this release.

### Added

- **Soft types & attributes** — runtime-defined `TypeDefinition` →
  `AttributeDefinition` → `AttributeValue` over an opaque `entity_id`, with 14
  data types and constraints (min/max length, min/max value, RE2 pattern,
  one-of; required / multi-valued / unique flags).
- **Attribute dependencies** — cascading picklists and conditional validation
  (equals / in / range / pattern / dynamic-time), resolved as a per-entity
  effective schema.
- **Type inheritance** — single-inheritance hierarchies with hierarchy-wide
  no-shadowing, subtype-anchored values, and cross-level dependencies and
  relationships.
- **Relationships** — user-defined directed (parent/child, role labels,
  per-side version pinning) and symmetric (unordered peer) relationship types,
  each with their own attributes, constraints, definition inheritance, and
  cardinality limits.
- **Localized & channel-scoped values** — a value can vary per locale and
  channel; uniqueness and FQL filtering apply per scope, and a query can pin a
  scope.
- **Computed attributes** — read-only attributes derived from a formula over an
  entity's other values, materialized as ordinary FQL-queryable values that
  stay in sync via an event subscriber (with dependency-cycle rejection).
- **Units of measure** — quantity attributes backed by tenant unit families;
  values convert to a base unit for comparison (exact rational conversion) with
  the original unit preserved for display, and FQL accepts unit suffixes.
- **Media attributes** — file values backed by a pluggable blob store (local
  disk or S3-compatible), with sniffed-MIME and size constraints and
  garbage-collection of superseded/erased blobs.
- **Entity revisions** — immutable point-in-time snapshots with as-of reads,
  diff, and restore (scope-aware); history is never mutated.
- **Change management** — draft → review → approve → publish change-sets with
  separation-of-duties approval and scheduled publishing.
- **Duplicate detection** — per-type match rules (exact, case-insensitive,
  trigram) producing scored, dismissable candidate pairs, scored identically on
  both backends.
- **Faceted grid & saved views** — attribute-column projection (no N+1), value
  facets over the current result set, and persisted views.
- **CSV import/export** — column-mapped import with dry-run and best-effort /
  transactional modes (required fields enforced); export honours the active FQL
  query.
- **Schema templates & cloning** — a lossless portable schema bundle, type
  cloning, and curated go:embedded starter templates.
- **FQL** — a schema-aware query language (comparisons, `in`, `range`, `has`,
  `length`, `min`/`max`/`count`, case-sensitive and insensitive string
  matching, boolean nesting with three-valued NULL logic, `type isa`,
  `child()`/`parent()`/`linked()` traversals, `matches()` full-text) executed
  identically over PostgreSQL and the in-memory store.
- **Read-only GraphQL API** — a Relay-connection schema generated from the live
  type definitions (edges/node/cursor, `pageInfo`, on-demand `totalCount`, FQL
  `filter` argument), ACL-filtered and free of N+1 loads.
- **Keyset pagination** — every listing uses cursor pagination stable under
  concurrent inserts and deletes, with on-demand total counts.
- **Field-level access control** — per-attribute read/write permissions on
  service accounts, enforced through the value read/write paths, effective
  schema, grid/facets/export, and the FQL binder (an unreadable attribute is
  invisible, not leaked).
- **Data erasure** — audited, admin-scoped hard purge of an entity's or a
  tenant's data (values, revisions, links, media blobs) for right-to-erasure
  compliance.
- **Domain events & delivery** — a typed dispatcher fanning a stable JSON
  envelope to consumer hooks; a transactional outbox with gap-free feed
  sequencing, managed HMAC-signed webhook subscriptions (backoff,
  dead-lettering, redrive, SSRF guard), a cursor-paged events feed with SSE
  tail and CAS cursors, and a Google Cloud Pub/Sub publisher.
- **Activity log** — every change audited with JSON before/after descriptors,
  written in the same transaction as the change.
- **Search index** (optional) — an event-driven per-entity projection powering
  FQL `matches()`, with trigram-accelerated `contains`.
- **Admin console** — a Vue 3 SPA for modelling types, attributes, dependencies
  and relationships; browsing entities with dependency-aware editing; import,
  revisions, change-sets, duplicates, the faceted grid, a GraphQL explorer, and
  operations — bearer-token sign-in, keyboard-accessible and responsive.
- **WebAssembly playground** — the whole service compiled to WASM over the
  in-memory store, hosted on GitHub Pages.
- **First-party Go client SDK** — `github.com/zkrebbekx/flexitype/client`, a
  standard-library-only module mirroring the embedded usecase surface over
  REST, conformance-tested against the real handler.
- **OpenAPI 3 contract** — the complete REST surface documented at
  `api/openapi.yaml` and served at `/api/v1/openapi.{json,yaml}`, with a CI
  route-coverage guard.
- **Deployment shapes** — embedded Go library and standalone service (versioned
  REST API, service-account auth with runtime provisioning, OpenTelemetry,
  Prometheus metrics, rate limiting, health endpoints), multi-tenant from day
  one, shipped as static binaries and a GHCR container image.

### Security

- Tenant isolation enforced on every by-ID interactor path.
- Field-level ACL enforced across grid, facets and CSV export (not only the
  single-entity read path).
- `FLEXITYPE_REQUIRE_AUTH` refuses to boot without an account source; the
  service stamps the principal's access explicitly rather than defaulting open.
- FQL parser recursion and query size are bounded (a deeply nested query
  returns a validation error rather than crashing the process).
- Media uploads are validated against the sniffed content type, not the
  client-declared one.
- The webhook SSRF guard validates the actual connect-time IP via the dialer
  control hook, closing a DNS-rebinding window; it blocks private, loopback,
  link-local and cloud-metadata targets, overridable for on-prem.
- `sslmode=disable` is refused for a non-loopback database host.

### Fixed

- Revision restore/diff preserve locale/channel scope instead of collapsing
  scoped values onto the base value.
- In-memory keyset pagination compares cursors by value, staying stable when
  the cursor row is updated or deleted between pages.
- Decimal and JSON uniqueness compare numerically / structurally on both
  backends (Postgres no longer admits `1.5` vs `1.50` as distinct).
- Committed writes are not failed by a post-commit subscriber error in the
  default delivery mode.
- Quantity `one_of` members and defaults are unit-rebased; equal quantities in
  different units compare equal.

[Unreleased]: https://github.com/zkrebbekx/flexitype/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/zkrebbekx/flexitype/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/zkrebbekx/flexitype/releases/tag/v1.0.0

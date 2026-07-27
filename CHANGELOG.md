# Changelog

All notable changes to flexitype are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0
(see [API stability](docs/api-stability.md)).

## [Unreleased]

### Security — field ACL is enforced on every value surface

The per-attribute permission set was applied by the value API, the grid,
facets, CSV export and FQL, but five other surfaces returned the same values
unfiltered. A principal restricted from reading an attribute could read it
anyway through any of them. Review before upgrading: a restricted principal
now sees less than it did.

- `GET /api/v1/values` applies the permission set. It was the only value read
  path that did not, so passing `attribute_definition_id` returned a
  restricted attribute's values for every entity in the tenant (#275).
- Revisions (`Get`, `AsOf`, `Diff`) omit unreadable attributes. Capture stays
  complete, so an admin restore still replays every value (#280).
- The activity log and the events feed mask value fields instead of returning
  them. The audit skeleton, the envelope and the feed sequence are preserved,
  so cursors stay gap-free; a masked record carries `"redacted": true` (#280).
- Media download requires read permission on an attribute referencing the
  object key, not only tenant ownership of it (#280).
- Duplicate detection requires read permission on a match rule's attribute,
  at rule creation and again on every scan (#280).
- Removing an entity requires write permission on every attribute it holds.
  It previously archived them all under a tenant check alone, so a principal
  barred from writing one attribute could erase it by removing the entity
  (#280).

### Performance — query shapes that no index could serve

Six hot paths used predicates the planner cannot index. Each returned correct
results, so each survived review; the difference is only visible in the plan.
`infrastructure/postgres/plan_integration_test.go` now pins every one of them
to the index that serves it, and EXPLAINs the shape it replaced alongside, so
the difference is recorded rather than asserted as an unexplained constant.

- FQL always emitted `type_definition_id = ANY(...)`, even for a single root
  type, so the entity-summary ordering index could not supply the ordering and
  every query scanned the tenant's whole entity population and sorted it. It
  now compiles to an equality for a single type, reusing the helper the entity
  browser already used. Measured at 200k entities: 57 ms with an external merge
  sort on disk, against 0.15 ms as an index-only scan (#331).
- `contains` / `icontains` compiled to `strpos(...) > 0`, which is opaque to
  the planner, so the substring test could only ever be a per-row filter.
  Migration 000018 had diagnosed this and resolved it by dropping the trigram
  indexes, leaving a documented operator with no index support at all. They now
  compile to `LIKE` / `ILIKE` with `%`, `_` and `\` escaped in the needle, and
  000021 restores the `gin_trgm_ops` indexes, which do serve them (#333).
- Archived-inclusive entity lookups had no usable index: every entity-leading
  index is partial on `archived_at IS NULL`, while `PurgeEntity` and media
  download include archived rows by design. Both sequentially scanned the value
  table — 573 ms to delete 20 rows at 4.16M rows, and 252 ms per media
  authorization, which a gallery page issues dozens of times. 000021 adds a
  non-partial `(tenant_id, entity_id)` index and an expression index on the
  media object key (#332).
- The FQL value scope probed `(tenant_id, entity_id)` and filtered the
  attribute afterwards, so every candidate entity's whole value set was read.
  000021 adds `(tenant_id, entity_id, attribute_definition_id)` (#333).
- `CountLiveLinks` put the entity only in an aggregate `FILTER`, never in the
  `WHERE` clause, so cardinality enforcement counted every live link of the
  relationship definition rather than the endpoint's own. It runs twice per
  link create. Measured at 400k links: 15.6 ms against 0.29 ms (#334).
- `flexitype_webhook_delivery.envelope_id` is an `ON DELETE CASCADE` foreign
  key with no index — Postgres never indexes the referencing side — so every
  pruned envelope sequentially scanned the delivery table at 13.7 ms each.
  000021 adds the index the cascade and the pruner's anti-join both need
  (#335).
- Outbox expansion loaded every active webhook subscription in the database on
  every pass, filtered by tenant in Go, and did so while holding the global
  expansion lock — so delivery throughput tracked total tenant count rather
  than event volume. It now filters by the claimed batch's tenants in SQL,
  selects only the three columns matching needs (signing secrets no longer
  cross the wire on this path), and 000021 adds the supporting index (#340).

### Security — authentication is required by default

`FLEXITYPE_REQUIRE_AUTH` defaulted to `false`, so a deployment that set only
database variables served the entire multi-tenant API to anonymous callers with
admin access — including `POST /api/v1/admin/purge`, the irreversible hard
delete. Every symptom of correct operation was present: health checks passed,
the console worked, and the only signal was one warning line at startup.

**This is a breaking change for any deployment that relied on the old
default.** A service with no account source now refuses to boot.

- `FLEXITYPE_REQUIRE_AUTH` defaults to `true` (#328).
- `FLEXITYPE_DEV_INSECURE=true` is the explicit opt-out, so the insecure
  configuration has to be asked for by name in a manifest rather than being
  what a deployment gets by forgetting a variable. It also permits
  `FLEXITYPE_DB_SSLMODE=disable` against a non-loopback host (#328, #284).
- The compose quickstart sets it, and boots again. It could not: the compose
  file uses the container hostname `postgres`, which the loopback-only SSL
  guard refused, so `docker compose up --build` crash-looped on config
  validation. CI now boots the compose stack, so the documented first-touch
  experience stays exercised — it was never covered, because CI only ever
  booted against `localhost` (#284).

### Changed — API contract and deployment topology

- **An unknown path under `/api/` is a JSON 404.** The admin console was
  mounted as the global `NotFound` handler, so a typo'd or removed endpoint
  answered `200` with `text/html`: "endpoint absent" and "endpoint succeeded"
  were the same response, a client that checks the status before parsing
  reported an HTML parse error rather than the real cause, and a misconfigured
  caller never appeared on an error-rate dashboard. Only non-API paths reach
  the app shell (#321).
- **The access log records the requested path.** The SPA handler rewrote
  `r.URL.Path` to `/` in place, before the logger ran, so every console
  response logged as `/` and an unmatched path could not be diagnosed after the
  fact. It now serves a clone (#321).
- **`FLEXITYPE_ENABLE_CONSOLE=false` gives an API-only deployment.** The SPA is
  not mounted at all, and an unmatched path returns a JSON 404 (#323).
- **`FLEXITYPE_RUN_RELAY`, `_RUN_DELIVERY_WORKER`, `_RUN_PRUNER` and
  `_RUN_SCHEDULER`** select which background loops a process runs, all
  defaulting to true. One image can now serve as an API tier and a worker tier
  with different resource profiles, autoscaling signals and disruption budgets.
  No leader election is involved: every loop claims work with a lease and
  `FOR UPDATE SKIP LOCKED`. Embedders pass `flexitype.DeliveryLoops` to
  `RunOutboxRelay` (#322).
- A malformed environment value is now reported before the account-source
  requirement, so a typo names the variable it is in rather than being masked.

### Fixed — concurrency invariants

Four check-then-write invariants were reported as racy. Two were, two were
already serialized by a row lock the reports missed. All four now have a
concurrent regression test, so which is which stops being a matter of reading.

- Revision `seq` was allocated as `MAX(seq)+1` outside any transaction, so two
  concurrent snapshots of one entity took the same number. Both readers order
  by `seq` alone, so a point-in-time read or a restore then picked whichever
  row Postgres returned first — non-deterministically, and differently between
  replicas. Allocation and insert now share a transaction with an advisory lock
  on the entity, and both readers break ties on `id` so any pre-existing
  duplicates at least read the same way twice (#337).
- The entity-summary trigger from 000019 upserted a shared summary row per
  affected value row, in statement order. Two writes to DISJOINT value rows of
  the same two entities, in opposite order, deadlocked on the summary rows —
  a conflict impossible before the projection existed. It is now three
  statement-level triggers that sort the affected keys, and `SetBatch` applies
  its items in a canonical entity order, so every writer takes the summary rows
  in the same sequence. Sorting also collapses the per-row work a bulk delete
  repeated (#330).
- Unique attributes (#329) and relationship max-cardinality (#338) were
  reported as count-then-insert races. They are not reachable: every value
  write takes `SELECT ... FOR UPDATE` on the attribute definition, and every
  link create takes it on the relationship definition, in the same transaction
  and before the count. Writers of one attribute — or one relationship
  definition — therefore serialize, and the second writer's count observes the
  first writer's committed row. Verified by removing the locks under a
  concurrent test, which stays green, while the same test reproduces the
  revision-seq race 3 runs out of 3. Both sites now say what makes them safe,
  so a change that weakens the definition lock is understood to reintroduce the
  race.

### Changed — migrations no longer block a live fleet

An upgrade used to apply every pending migration in one transaction. That form
cannot carry the statements a large deployment needs, and migration 000019 made
the consequence concrete: `CREATE TRIGGER` on the value table takes
`SHARE ROW EXCLUSIVE`, and holding it through the whole-table backfill in the
same transaction blocked every value write in the fleet for the duration —
with health checks still green, because reads were unaffected.

- Each migration now applies in **its own transaction**, in version order,
  under a session-scoped advisory lock held for the whole run. A failure
  part-way leaves earlier migrations applied and resumes at the failing one
  (#319, #339).
- A migration file may declare `-- +flexitype:no-transaction`, which runs its
  statements one at a time outside any transaction. Migration 000018 now uses
  it to build its value-table indexes `CONCURRENTLY` instead of taking a
  write-blocking lock for the whole build (#319).
- Data movement moved out of `.sql` files into **backfill steps** that run
  after the schema is in place, in bounded batches, each committing on its own
  and tracked in `flexitype_schema_backfill`. Migration 000019's whole-table
  aggregate is now the `000019_entity_summary` step. Its trigger keeps new
  writes correct from the moment it exists, so the backfill only catches up
  history and may run while the fleet serves traffic (#339).
- A binary that finds schema versions it does not carry logs a warning at
  startup, so a mixed-version fleet is visible during a rolling deploy rather
  than inferred. Embedders read the same fact with `Service.SchemaDrift`
  (#319).
- `docs/upgrades.md` states the rolling-upgrade and rollback contract: release
  N's migrations stay compatible with release N-1's binary, rollback is
  redeploying the previous binary, and `MigrateDown` is a development tool. It
  also lists the rules a migration must follow, two of which CI enforces
  (#319).

### Added

- `uow.Access.Default` sets the level for an attribute the permission set does
  not name. Its zero value keeps the existing deny-list behaviour; setting it
  to `uow.PermNone` makes the set an allow-list, so an attribute added later
  is unreadable until it is granted.
- `flexitype.WithFailClosedACL()` inverts the field-ACL default for embedders:
  a request that carries no `uow.Access` policy denies every attribute instead
  of granting admin. Use `uow.WithSystemAccess` for host-owned background work
  that has no principal (#298).

### Fixed

- The Postgres-backed test packages no longer share one schema. Go runs
  different packages' tests in parallel and each suite truncated and seeded the
  same table names, so `go test ./...` failed intermittently with a
  duplicate-key error deep inside a fixture helper, in a test unrelated to the
  change under review. `internal/testdb` now gives each package its own schema,
  which removes the shared state rather than serializing around it (#297).
- The migration advisory lock is keyed by schema instead of being a constant,
  so two flexitype schemas in one database no longer serialize against each
  other's upgrades.

- `Repository.GetMany` issues one statement for the whole key set. Outside a
  transaction it awaited each dataloader thunk before requesting the next, so
  every key closed its own batch and paid the full 2 ms batch window — N round
  trips for a call whose contract is one. It runs on every non-admin value
  read, so restricting a principal's permissions made its reads dramatically
  slower than the admin path over identical data (#341).
- `Repository.GetMany` skips an id with no row instead of returning NotFound.
  An activity entry or a feed envelope can name an attribute a purge has since
  removed, and one such row must not fail the whole page. Callers decide what a
  miss means: the search indexer skips the value, the field ACL treats the
  attribute as unreadable (#341).

## [1.2.0] — 2026-07-18

### Changed — REST behaviour

Three inconsistencies where the API contradicted itself, released as a minor
because in each case the documented contract was already the new behaviour —
the old one was the defect. Each is still visible to clients, so review before
upgrading; the first can reject requests that previously succeeded.

- **Declared numeric types are enforced, not coerced.** An `integer` or `float`
  attribute now rejects a quoted number (`"5"`, `"1.5"`) with `422 VALIDATION`.
  Previously `encoding/json` unmarshalled these straight into a `json.Number`
  and they were accepted, so the declared type meant nothing at the boundary.
  `decimal` is deliberately unchanged: it accepts a string form on purpose, to
  carry exact precision without float rounding. Clients sending quoted numerics
  to integer/float attributes must send bare JSON numbers.
- **`DELETE` of a missing unit family or match rule is `404`, not `204`.** These
  two were the only by-id routes reporting success for something that was never
  there; saved views, service-account revoke, unlink, value removal and
  dependency archival all already returned `404`. The existence check is
  tenant-scoped, so another tenant's record stays indistinguishable from a
  missing one.
- **Provisioning routes authorize before reporting the feature gate.** A caller
  without the admin scope now gets `403` rather than `501 FEATURE_DISABLED`, so
  an unauthorized caller cannot learn whether provisioning is configured in this
  deployment. Matches the order the other protected routes already use.

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

[Unreleased]: https://github.com/zkrebbekx/flexitype/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/zkrebbekx/flexitype/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/zkrebbekx/flexitype/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/zkrebbekx/flexitype/releases/tag/v1.0.0

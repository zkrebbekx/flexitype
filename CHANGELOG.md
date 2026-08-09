# Changelog

All notable changes to flexitype are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0
(see [API stability](docs/api-stability.md)).

## [Unreleased]

### Added — A read-only cross-tenant credential ([#549])

The `read_any_tenant` scope gives ONE service account that reads EVERY tenant
and writes none. The tenant to read travels in the `X-Flexitype-Tenant` header,
which is read only for such an account — for every other credential the tenant
still comes from the token and the header is ignored, so it can never widen an
ordinary one.

A tenant comes from the token, and there is no request that reads two. That is
a property rather than a gap, but it made every cross-tenant READ MODEL — a
marketplace storefront, a group-wide search index, a billing rollup — hold one
full read/write credential PER TENANT purely to re-read entities.
Concentrating every tenant's credential in one service is a far larger
exposure than the read it needs.

The narrowness is enforced twice, because the difference between a credential
that can read every tenant and one that can rewrite every tenant is worth two
checks: an account holding `read_any_tenant` may not also hold `write` or
`admin` (refused when it is created), and the API refuses every mutating
method for such an account whatever else it holds.

`client.ForTenant` and the TypeScript client's `tenant` option send the header.
The marketplace example now mints one reader at start-up and its storefront
holds no merchant token for a read.

### Changed — Minting a signed media link is a GET ([#552])

`GET /api/v1/media/{key}/signed-url`, with `?ttl_seconds=`. It was a POST,
which changes nothing and locked the endpoint away from exactly the credential
with the strongest reason to want a link: a read-only cross-tenant reader.
Minting hands back a capability to read an object the caller can already read,
so it is a read. The feature is unreleased, so nothing depends on the old
shape.

### Added — Signed, expiring media links ([#552])

`GET /api/v1/media/{key}/signed-url` mints a link, and
`GET /media/signed/{token}` serves the bytes to anyone holding it — outside
`/api/v1`, with no credential, because the signature IS the credential. Turn it
on with `FLEXITYPE_MEDIA_URL_SECRET`, or `APIConfig.MediaURLSecret` when
embedding. `client.SignMediaURL` and the TypeScript SDK's
`entities.signMediaUrl` call it.

Media bytes are served behind the same authentication as everything else, and a
token carries one tenant, so a public surface — a storefront, a catalogue page,
an email — could not link to an image. It had to proxy every request through a
service holding a tenant credential, which carries the whole file through a
process with no other reason to touch it and defeats any CDN in front of it.

The rules the design holds to: the signature covers the tenant, the object key
AND the expiry together; the tenant is read back out of the verified token
rather than the request; minting is gated on the same ownership and
field-permission check the authenticated download makes; every redemption
failure is the same 404; the default life is 15 minutes and the cap is 24
hours; and the secret must be at least 32 characters and must not be the
webhook signing key.

Without the secret the endpoint answers `501 FEATURE_DISABLED` rather than
pretending to work, and a secret below the floor stops the service at start-up.

### Added — An event envelope says which entity changed ([#550])

`type_definition_id` and `entity_id` are on the envelope. They name the ENTITY
an event concerns, so a consumer routes on "entity E changed, re-read it"
without decoding the payload.

`aggregate_id` names the aggregate that EMITTED the event, which for a value
event is the attribute value — a different thing. A router that wanted the
entity therefore had to decode the payload of every event type it routed,
which couples it to a payload schema it should never have to know. The
marketplace example decoded three payload types for that one fact.

They are absent for an event that concerns no entity (a schema change) or more
than one (a relationship link, which names two endpoints): one pair of fields
cannot honestly describe two.

The change is additive, on the envelope and in both clients. Migration 000038
adds the two columns to the outbox, so the coordinates survive storage and
reach a webhook subscriber; a row written before it reads back with them empty,
and a consumer that falls back to the payload keeps working.

## [1.5.0] — 2026-08-09

Two releases' worth of work in one: a defect-review release, and the release
that gives the API a TypeScript client.

An evaluating team read the 1.4.0 tree and filed 40 issues; all of them are
fixed here, together with the two patch releases the rollback contract needed
(v1.3.1, v1.4.1). Four were field-permission disclosures, and several were
silent data loss.

Building a full example on top of the result — a multi-tenant marketplace, in
[examples/marketplace/](examples/marketplace/) — then found eight more things,
of which the four that are deployment defects are fixed here too. The rest are
filed: [#549], [#550], [#551], [#552].

### BREAKING — check before upgrading

Ten behaviour changes are visible from outside. Each is the documented
contract being honoured, and most refuse a request that previously succeeded
— the carve-out `docs/api-stability.md` states. No supported Go signature
changed: the ports that gained methods (`admin.Store`) or arguments are the
store and repository ports that page names internal.

- **A context condition must declare `context_type`.** *(Refuses what
  previously succeeded.)* A `context_key` condition was validated against the
  SOURCE attribute's type while its subject at evaluation is the
  caller-supplied fact, so a range over a numeric fact was unbuildable unless
  the unrelated source attribute happened to match, and a fact arriving as
  another type turned every write to the target into a 500. Declare the
  fact's type; a fact whose runtime type differs no longer matches, rather
  than erroring. ([#514])
- **A computed attribute may not be multi-valued.** *(Refuses what previously
  succeeded.)* The materializer writes one result per entity and a
  multi-valued write appends, so results accumulated with nothing able to
  clear them. An attribute already carrying both is refused on its next
  update until the combination is resolved. ([#529])
- **A symmetric relationship may not declare `min_parents`/`max_parents`.**
  *(Refuses what previously succeeded.)* Both were accepted and then ignored:
  a symmetric `spouse_of` capped at one parent admitted three links. Migration
  000035 folds a stored parents bound into the children bound — the side that
  IS enforced — keeping the tighter of the two. ([#530])
- **`min_exclusive`/`max_exclusive` apply to a range condition only.**
  *(Refuses what previously succeeded.)* On `equals`, `in`, `pattern` and
  `dynamic` they validated, stored and did nothing, while the OpenAPI schema
  documented them on the shared condition object. ([#531])
- **A quantity attribute's unit family cannot be cleared or deleted while
  values reference it.** *(Refuses what previously succeeded.)* A REST `PUT`
  omitting the `omitempty` `unit_family_id` cleared the family, and deleting
  one checked only that it existed; either left values readable and never
  writable. ([#527])
- **`linked()` refuses a name both endpoints declare.** *(Refuses what
  previously returned an empty page.)* The union kept the parent's attribute,
  so from the parent side the condition tested the wrong one and matched
  nothing. Use `child()`/`parent()` to pick a side. ([#536])
- **CSV import no longer reads the legacy in-band multi-value forms.** A cell
  is multi-valued only when it carries the `#flexitype-values:` prefix. Both
  legacy forms are shapes an ordinary value can have, so a `string` value
  holding the literal `[{"value":"a"},{"value":"b"}]` imported as TWO values
  — this tool's own export did not round-trip. Set
  `allow_legacy_multi_value_cells` on the import to read a file an earlier
  release wrote. ([#528])
- **A deactivated webhook subscription stops its queued backlog.** Turning
  one off previously stopped new fan-out only, so the endpoint kept being
  called. The backlog RESTS rather than dies: rows stay pending, reactivating
  resumes them, and the retention pruner bounds them. ([#531])
- **Single-row `Redeliver` resets the attempt budget and refuses an inflight
  delivery.** It left `attempts` at the cap, so a redriven delivery died
  again on its first failure, and its unguarded `UPDATE` could rewind a
  delivery a worker had just claimed, sending it twice. ([#531], [#524])
- **`matches()` searches only what the caller may read.** For a
  field-restricted principal it now searches the attributes that principal
  can read, and returns fewer rows than it did in 1.4.0 — where it searched
  every value and leaked restricted content word by word. ([#511], [#541])

### Added — A TypeScript client and React hooks ([client-ts/](client-ts/))

`client-ts/` holds `@flexitype/client`, a TypeScript client for the REST API
with a second entry point, `@flexitype/client/react`, for React hooks. It is
**not published to npm**: it lives in this repository and moves with the API,
so the client that ships in a release matches that release. See
[client-ts/README.md](client-ts/README.md).

The core entry point is framework-agnostic, has no runtime dependencies and
imports no React, so the Vue console can adopt it. It carries typed errors
with the same machine codes as the Go client, keyset pagination as both a
single page and an async iterator, a retry policy that mirrors
`client/retry.go`, and soft-type helpers: effective-attribute resolution, a
form descriptor, per-data-type value coercion and formatting, and scoped
(locale and channel) value addressing.

Its data shapes are generated from `api/openapi.yaml`; a test regenerates them
and fails when the checked-in output is stale. `TestErrorCodeContract` now
holds four lists equal rather than three, the fourth being the TypeScript
`ERROR_CODES` array.

### Fixed — The OpenAPI document now describes fields the API already accepted

The document omitted attribute fields the service reads and writes —
`localizable`, `scopable`, `unit_family_id`, `display_unit` and `computed` —
and the `locale` and `channel` of a value, which is how a scoped value is
addressed. Constraints, defaults and dependency operands were typed as a bare
object, which a code generator renders as a field that cannot be filled. The
`TypedValue`, `Constraint`, `DynamicValue`, `DefaultValue`, `Computed`,
`Rollup`, `CreateDependency` and `UpdateDependency` schemas are now declared
and referenced. The change is additive; `TestResponseContract` validates the
live handler against the tightened schemas.

### Added — A bootstrap admin credential a manifest can know ([#547])

`FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN` sets the credential the first admin account
takes, instead of the service minting one. `flexitype bootstrap-token` prints a
valid token for an operator to store in a secret manager.

A minted token is printed to stdout once and the shipped image is distroless,
so an orchestrated deployment could never capture it: every service starts at
the same moment, and the service that needs the admin credential needs it
before the log line exists. The only way through was to scrape a container log,
which is not an interface.

The supplied token must be one flexitype would have minted — `ft_<ULID>_<secret>`
with a secret of at least 32 characters — so a hand-written or weak value is
refused at startup rather than at exploitation. It applies on FIRST boot only:
a tenant that already has an account is left alone, so an environment variable
cannot re-key a live deployment. It is never logged and never returned.

`Service.BootstrapAdminWithToken` is the embedded equivalent.
`admin.CreateAccountInput` gained `ID` and `Secret`, which must be supplied
together.

### Added — `FLEXITYPE_DB_ALLOW_PLAINTEXT` ([#548])

It permits an unencrypted database connection to a non-loopback host, and
nothing else. `FLEXITYPE_DEV_INSECURE` was the only opt-out from that guard,
and it ALSO disables authentication, so a stack that needed plaintext Postgres
over a container network had to set the variable that turns authentication off
in a deployment where authentication stays on. `FLEXITYPE_DEV_INSECURE` still
implies the new one, so no manifest breaks. The two conditions are now logged
separately.

### Fixed — A non-writable blob directory stops the service at start-up ([#553])

`blob.NewDiskStore` proved nothing: `os.MkdirAll` returns nil for a directory
that already exists, whatever its ownership. A named volume mounts owned by
root and the shipped image runs as `nonroot`, so a configured
`FLEXITYPE_BLOB_DIR` the process could not write to built a store, logged
"media storage enabled", passed every health check, and failed on the first
upload — hours later, and only for whoever uploaded it.

The store now creates and removes a probe file when it is built, and names the
directory and the running uid when it cannot. A configuration error stops the
service, which is the rule the authentication guard already followed.

### Fixed — `client.WebhooksService.Update` can now succeed

The method sent `SubscriptionInput`, the CREATE body, to
`PATCH /webhook-subscriptions/{id}`. That handler decodes with
`DisallowUnknownFields` and accepts `url`, `event_types`, `active` and
`rotate_secret`. `SubscriptionInput` carries a `name` with no `omitempty`,
and calls its secret `secret`, so the body always held a key the handler
rejects: every call returned `VALIDATION: invalid request body`, whatever it
was given. The method now sends only the four fields the API accepts and maps
`Secret` to `rotate_secret`.

A `Name` is refused with a validation error rather than dropped, because the
API cannot rename a subscription — reporting success for a rename that did
not happen is the worse failure. The signature is unchanged, and no working
caller exists to break.

A conformance case drives the method against the real handler, so it cannot
regress to silence.

### Security

- **A computed attribute was a full field-ACL read bypass.** A principal
  denied an attribute could declare a formula over it and read the exact
  values back through the derived attribute, which is born unrestricted and
  materialized under system access. The formula guard now refuses a source
  the caller cannot read, worded identically to an unresolved reference so
  the guard is not itself an existence oracle. ([#509])
- **Computed materialization ran under the writing principal's field ACL.**
  The post-commit projection dispatch reused the request context, so a
  restricted input was redacted from the materializer's read and the wrong
  result — or a deletion of the right one — was written durably for every
  reader. Projections now dispatch under system access. ([#509])
- **The dependency surface enforced no field ACL.** `effective-schema`
  resolved rules over unredacted values, so a rule keyed on a restricted
  source recovered its exact value by binary search in about twenty requests
  per entity; `completeness` disclosed restricted attribute names and counted
  them in the score. ([#510])
- **`matches()` searched restricted values.** The search document flattened
  every textual value with no attribute identity, so a denied principal
  enumerated restricted content word by word. Fixed by failing closed
  ([#511]), then restored per attribute ([#541]).

### Added

- **Declared default values are applied.** `Values().ApplyDefaults` (REST:
  `POST /entities/{typeDefinitionID}/{entityID}/apply-defaults`; SDK:
  `Entities().ApplyDefaults`) writes every declared default the entity does
  not hold, resolving a dynamic default at the moment of the call. Defaults
  were previously stored, exported and rendered, and never reached an entity.
  Base scope only; a computed attribute is skipped. ([#533])
- **Parked outbox envelopes are visible, redrivable, alarmable and
  prunable.** `GET /admin/outbox/parked` and `POST /admin/outbox/redrive`,
  a parked gauge separated from the pending metric, retention for parked
  rows, and configurable max-attempts and retry ceiling. A parked envelope
  was previously undeliverable, unprunable and counted as pending forever.
  ([#523])
- **An FQL query can page on the immutable key.** `ExecuteInput.Stable`, set
  by the grid's facet counts and by a filtered CSV export. Both are sweeps,
  and both silently dropped entities written mid-sweep. ([#539])
- **`matches()` serves a field-restricted principal again**, through
  per-attribute search vectors (migration 000037, backfilled from the stored
  document). ([#541])

### Fixed

- Restoring a revision after an entity's anchor narrowed archived nothing and
  recorded the entity as empty; the follow-up revision now carries the current
  anchor. ([#513])
- `ApplySnapshot` keyed its target on the rendering while the set pass
  compares with `Equal`, so restoring a snapshot that held decimal `1.5`
  against a stored `1.50` left the entity with NO value. ([#529])
- The make-unique guard grouped values by rendering while the write path
  compares them typed, so `1.5`/`1.50` and `5 kg`/`5000 g` passed the flip and
  then refused every later writer forever; it also ignored scope. ([#526])
- A saved view was un-updatable after its first update: `Get`/`List` omitted
  the `version` column, pinning the compare-and-swap to 1. ([#488])
- Media-blob GC used a cross-tenant unindexed count in a shared budget, raced
  an in-flight adoption, and could adopt a key whose bytes were already
  collected. ([#525])
- A tenant purge's residual redaction was one unbounded UPDATE over an
  unindexed JSON path, stalling event delivery fleet-wide while the relay
  waited holding the global expansion lock. ([#534])
- Schema import collapsed a cascading picklist: dependencies were keyed on
  the endpoint pair, so every rule after the first on one pair was skipped as
  "already present". ([#516])
- Migration 000030's invalid-index guard was namespace-blind with an
  unqualified DROP, and the migration lease was never renewed mid-statement,
  so a long concurrent index build could thrash on a rolling deploy.
  ([#517])
- Dependency `Update`/`Archive` did not lock the target attribute, admitting a
  value the tightened rule forbids, permanently. ([#514])
- The entity-summary refresh counted before taking the row lock, persisting a
  stale undercount under concurrent same-entity writes (migration 000032).
  ([#520])
- The traversal counterpart-type probe was a `LIMIT 1` with no `ORDER BY`, so
  an `is type` condition could match or not match across executions. ([#524])
- A symmetric listing returned one counterpart twice and skipped another at a
  page boundary; the total counted both. ([#540])
- The console dependency editor stripped `context_key` on save, the effect
  editor dropped `one_of` and downgraded substring patterns, and the type page
  showed "No dependencies" while its query was pending or failed. ([#518])
- The value grid collapsed a multi-valued attribute to one arbitrary cell
  while the facet counts beside it counted every member. ([#531])
- Six further minor defects from the review. Decimal extended statistics are
  documented as a known residual rather than fixed: expression statistics on
  `(value_text)::numeric` make `ANALYZE` fail on the first non-numeric text
  value, which would break autovacuum for the whole table. ([#531])

### Changed

- The SaaS-shape load test migration 000030 cites is committed, behind the
  `stress` build tag. ([#521])

The rollback-contract change and the v1.3.1/v1.4.1 patch releases have their
own entry below.


### Fixed — An unusable pagination cursor is now a 422, not a 500 or a silent restart ([#502])

`PageArgs.Resolve` checked the cursor's shape only: base64 that decodes to
a JSON string array. It did not know the ordering columns, so two
well-formed cursors carrying unusable values went wrong further down.

- A cursor whose value the column's cast cannot parse reached the cast in
  the compiled query. PostgreSQL failed with SQLSTATE 22007 and the API
  answered **500 INTERNAL**, with the cursor's contents in the message. It
  now answers **422 VALIDATION**.
- A cursor with the wrong number of values was discarded, so the query ran
  with no keyset predicate and re-served page 1 to a client that believed
  it was advancing. It is now rejected with **422 VALIDATION**.

`db.ValidateKeyset` performs the check. It parses each cursor value against
the type the column's cast implies: a timestamp cast accepts what
`db.KeysetTime` emits, plus RFC 3339 and a date-only value; a numeric cast
must parse as a number; a column with no cast holds text and accepts any
string. The error message never repeats the cursor's contents. Both
backends now reject the same cursors — the in-memory backend used to
compare a bad timestamp as a plain string and answer a page.

The cost is one parse for each cast column, once for each query, and only
when the request carries a cursor. A request with no cursor is unchanged.

This is the documented carve-out in [API stability](docs/api-stability.md):
a request that previously succeeded (the silent restart), or failed as a
500, now fails as a 422.

### Fixed — The admin control plane records every credential and role change ([#507])

`CreateTenant`, `SetTenantActive`, `CreateAccount`, `RotateSecret`, `Revoke`,
`UpsertRole`, `DeleteRole` and `AssignRoles` took no unit of work and wrote no
activity entry, so a leaked token had no provenance: "who created this account,
and who rotated it since?" had no answer. Each of them now runs as one
transaction and writes exactly one entry in it, stamped with the AFFECTED
tenant. The entry records that a secret changed, never the secret, its hash or
the minted token. Two check-then-act races close with it: `DeleteRole` locks
the role row exclusively before it counts the holders, and an assignment locks
the roles it names in shared mode before it writes the account row, so a grant
can no longer land between the count and the delete. See
[docs/design/identity.md](docs/design/identity.md).

### Fixed — Stored payloads join the rollback contract; patch releases v1.3.1 and v1.4.1 ([#497])

The "release N-1 runs correctly against release N's data" guarantee covered
columns but not JSON payloads: v1.3.0's condition decoder dropped v1.4.0's
`min_exclusive`/`max_exclusive` keys, so any rule update, clone or export by
a v1.3.0 binary during a rollback silently and permanently rewrote a strict
bound as inclusive. v1.3.1 preserves those keys, and v1.4.1 preserves the
upcoming `context_type` key, both as decode-and-re-encode passthrough with
no behavior change. Roll back to the patch release of the previous line,
never to its .0. The payload rule is now stated in
[docs/upgrades.md](docs/upgrades.md).

### Fixed — A query with both locale and channel skipped single-dimension attributes ([#474])

The FQL compiler pinned both `locale` and `channel` on any localizable or
scopable attribute. The write path stores an empty string in the dimension
an attribute does not carry, so a query that supplied both dimensions
excluded every value of a localizable-only or scopable-only attribute.
Locale now narrows only a localizable attribute. Channel now narrows only
a scopable one. Both backends apply the same rule.

### Fixed — Traversals matched value-less ghost counterparts over dangling links ([#475])

Removing an entity's last value hides it at the root, but its
relationships stay live. A traversal across such a dangling link matched
the ghost, so `count()=0`, `not has()` and negated type conditions
wrongly selected the near entity, and `type !=` diverged between the two
backends. Every traversal hop now requires the counterpart to be visible
in the entity-summary projection, on both backends. Migration 000031 adds
the `(tenant_id, entity_id)` index that serves the probe as one B-tree
lookup.

## [1.4.0] — 2026-08-07

### Added — Strict range bounds on dependency conditions ([#466])

A `range` condition accepts `min_exclusive` and `max_exclusive`. An
exclusive min reads "greater than"; an exclusive max reads "less than".
Before this, a bound was always inclusive, so "over 50000" had no exact
form on a float, decimal, date or quantity attribute — there is no next
value to name. The zero value keeps the inclusive semantics of every
stored rule. An exclusive flag without its bound is a validation error.
The client SDK and the OpenAPI schema carry the new fields.

### Added — A type-aware dependency builder in the console ([#466])

The builder now mirrors the API's own validation. It offers each condition
kind only to the source types that support it. It edits a range as a
comparator (between / > / ≥ / < / ≤). Every operand renders through the
typed value input, so dates get pickers, quantities get unit dropdowns,
and bools and enums get pick-lists. The effect side adds the constraint
editor the API always accepted but the UI never offered, and the pattern
condition exposes `pattern_substring`.

### Added — `WithClock`, a pinned evaluation clock ([#465])

`flexitype.WithClock(func() time.Time)` pins the instant that `today` and
`now` resolve against, for tests and simulations. It rides the context
exactly as `WithTimeZone` does: `Service.Context` and the API middleware
stamp it, and per-request `uow.WithClock` overrides it. The scope is
calendar evaluation only — stored timestamps keep the wall clock, so a
pinned clock cannot backdate an audit trail.

### Changed — Float value index and per-attribute statistics ([#467])

Migration 000030 adds the value index that `value_float` alone lacked, so
float and quantity comparisons can be driven from the value side, and adds
multi-column MCV statistics on each `(attribute_definition_id, value)`
pair, so the planner sees per-attribute selectivity instead of a blend of
every attribute sharing the column. Measured on a 20M-row dataset: a
rare float predicate's match set loads in under 2 ms (was a 200 ms walk),
and an integer equality dropped from 70 ms to 21 ms from the statistics
alone. The index builds concurrently; the upgrade does not block writes.

### Fixed — The type page's dependency list could stay empty ([#466])

The list filtered inside a query `select` that closed over the
effective-attributes query. When the dependency request resolved first,
the memoized result was empty and the page showed "No dependencies" until
an unrelated refetch. The filter is now a computed over both queries.

### Fixed — The `today`-rule test held only part of the day ([#465])

`TestTimeZoneReachesEvaluation` asserted an outcome that was a function of
the wall clock: before 10:00 UTC the test zone shares a date with UTC and
the assertion failed. The test now pins the clock through `WithClock`,
so the outcome is a constant.

### Fixed — The release workflow's nested-module tag probe

`gh api` writes its 404 body to **stdout**, so capturing the "does this tag
exist" probe with `|| true` put `{"message":"Not Found",…}` into the variable
that holds an existing tag's sha. Every FIRST-TIME nested-module tag therefore
failed as a conflict against a sha that was really an error message, and the
step is deliberately ordered before the release — so v1.3.0 built its binaries
and then stopped, with no GitHub release and no `client/v1.3.0` tag. Both were
completed by hand.

The probe now branches on the exit status and keeps the body out of the
variable, and `release_modules_test.go` pins that shape alongside the module
list it already holds.

## [1.3.0] — 2026-07-28

Twenty defects an independent reviewer raised against the 1.2 line, closed in
six pull requests. Two were P0: a right-to-erasure purge that could report
success with the data still present, and a change-set publish that a cancelled
request stranded with no API able to move it.

It is a minor release rather than a patch because the fixes add public API:
`Service.Context`, `uow.AccessFromPermissions`, `changeset.ClaimReclaimer` and
`changeset.PublishClaimTTL`, `computed.Materializer.OnFormulaError`,
`ratelimit.Limiter.Refund`, and `formula.Members` with `EvalWithMembers`,
`EvalRatWithMembers` and `NumericRefs`. Nothing existing changed shape.

### BREAKING — check before upgrading

Six behaviour changes are visible from outside. Each is the documented
contract being honoured, and two of them can refuse a request that previously
succeeded — the carve-out `docs/api-stability.md` states.

- **`FLEXITYPE_TIMEZONE` now reaches rule evaluation.** It previously reached
  nothing, so every `today`/`now` dependency rule and dynamic default resolved
  in UTC whatever the setting said. A deployment that set it will see
  date-boundary rules fire on the configured day — which is the point, and is
  a change from what it did yesterday. An embedder must pass its context
  through `Service.Context` (see `docs/embedding.md`); the API does it in
  middleware.
- **A formula may only read or fold a numeric attribute.** *(Refuses what
  previously succeeded.)* `sum`, `avg`, `min` and `max` over a `string`,
  `date`, `json` or other non-numeric source, and a bare read of one, are
  refused with `422` at create and update. They previously materialized `0` or
  nothing at all. `count()` accepts any type, because it folds members. A
  stored formula is not re-validated, so an existing definition keeps working
  until it is next written — the materializer reports it through the
  background-error observer instead.
- **A purge that cannot make progress reports an error.** *(Refuses what
  previously "succeeded".)* Completion is decided by a count of the remaining
  rows, not by a chunk that removed nothing, so an erasure that leaves rows
  behind fails loudly instead of returning a receipt.
- **CSV multi-value cells carry a `#flexitype-values:` prefix.** A tool that
  parses an export must handle it. Import still accepts both earlier formats
  for non-`json` columns, so old files load unchanged.
- **The pre-authentication rate limiter counts failed authentications**, not
  every request. A deployment behind a proxy will stop seeing `429` on healthy
  authenticated traffic.
- **`leaseWait` for migrations is 17 minutes**, up from 10, so a runner waits
  out an abandoned lease instead of exiting before it can expire.

### Fixed — A migration that is interrupted recovers, and an abandoned lease frees itself

- **A no-transaction migration reaps its own invalid indexes before it
  replays.** A failed `CREATE INDEX CONCURRENTLY` leaves an INVALID index
  behind. The name then exists, so `IF NOT EXISTS` skipped the rebuild for
  ever — and migration 000028 also drops the index it replaces, so a replay
  removed the working index and left only the invalid one. Every by-entity
  revision read then sequentially scanned the table, permanently, while the
  schema version read as current and nothing raised an error. The trigger is
  an ordinary pod lifecycle event during a deploy: an OOMKill, an eviction or
  a lock timeout part-way through the build. The reap covers every
  no-transaction migration (000018, 000021 and 000025 build indexes
  concurrently too), and is a no-op when nothing is invalid.
- **`leaseWait` now exceeds `leaseTTL`.** A runner that dies holding the
  migration lease leaves a row with a live `expires_at` that nobody renews.
  With a 10-minute wait against a 15-minute TTL, every replica booting inside
  that window gave up BEFORE the lease could expire — inside a container
  startup path, so the orchestrator restarted it and it repeated, and a whole
  generation could not boot. The wait is the TTL plus a two-minute margin, so
  a survivor outlasts the abandoned lease.
- **The acquire failure names the blocker.** It reports the current holder and
  the `expires_at` it is waiting on, so an operator can correlate the stall
  with the pod that died.
- **The lease is released on a detached context.** The commonest reason
  `Migrate` returns is that the caller's context ended, and releasing on that
  same context was a no-op — so an embedded deployment with a cancellable
  startup context stranded the lease for the full TTL while the process was
  alive and able to free it.

### Fixed — Four residual one-sided fixes: the CSV multi-value marker, the redrive ramp, saved-view locking and two documents

- **The multi-value CSV marker is out of band.** Any in-band sentinel drawn
  from the JSON grammar can be forged by a JSON payload: the format was a bare
  array of `{"value",…}` objects, and retagging it as `{"values":[…]}` moved
  WHICH documents collide rather than whether they can — the tagged shape is
  exactly what an export of a `json` column looks like, so re-importing this
  tool's own output read one document as two members, wrote both to a
  single-valued attribute, kept the last, and reported one row written with
  zero errors. A cell is now marked with a `#flexitype-values:` prefix, which
  no JSON document can begin with, so a multi-valued `json` column round-trips
  too. Both legacy forms are still accepted on import for non-`json` columns.
- **The redrive ramp is per subscription.** `ClaimDue` takes a subscription's
  single lowest-`feed_seq` pending row, and only if that row is due, so a
  per-row random offset was head-of-line blocking rather than smoothing:
  measured over 20 revived rows, the head drew +4m26s and nothing was
  claimable for four and a half minutes while 19 later deliveries were
  already due. The offset is now derived from the subscription id — identical
  for every row of one subscription, spread across the window between
  subscriptions.
- **Saved-view optimistic locking is reachable from a client.** `PATCH`
  accepts an optional `version`: send the one you read and a view someone else
  edited answers 409 instead of being overwritten. `Patch` re-read the view
  microseconds before writing it, so two users editing the same view both
  passed their own check and the second silently discarded the first. Omitting
  `version` keeps last-write-wins. The field is in the OpenAPI document and in
  the Go client (`SavedViewPatch.Version`, `SavedView.Version`).
- **Two documents now describe the code.** `docs/design/identity.md` no longer
  carries the pre-fix bullet calling a deleted role "the safe direction" — the
  belief the fail-open guard exists to refuse — and both it and
  `docs/configuration.md` state that auth-cache eviction is PER PROCESS: a
  multi-replica deployment converges over `FLEXITYPE_AUTH_CACHE_TTL`, so
  during an incident the TTL, not the API response, is when a revocation is in
  force everywhere.

### Fixed — Five one-sided guards: DSN quoting, the pre-auth limiter, the GraphQL schema key, the effective-permissions view and the time zone

- **The TLS guard parses the connection string with libpq's grammar.** The
  keyword form was split on whitespace and cut at the first `=`, so a
  single-quoted value kept its quotes: `sslmode='disable'` was compared as
  `'disable'`, matched nothing and passed a guard that exists to refuse
  exactly that — while lib/pq honoured the quoted form and connected in
  cleartext. `host` and `hostaddr` were evaluated unstripped for the same
  reason. Quoted values, backslash escapes and spaces around `=` are now read
  as libpq reads them.
- **The pre-authentication limiter charges failed authentications, not
  traffic.** A token is taken up front and refunded unless the response is
  401. Charging every request made the shipped 20 rps default a ceiling on
  ALL traffic behind an ingress or a `Cluster`-policy LoadBalancer, where
  every request appears to come from one address: healthy authenticated
  clients, well inside their per-account and per-tenant budgets, got 429.
  `pkg/ratelimit` gains `Limiter.Refund`.
- **The GraphQL schema-cache key covers `Access.Default`.** An empty `Attr`
  map signed as `"open"` whatever the default was, so `uow.DenyAll()` — what
  an account naming a deleted role resolves to — collided with an
  unrestricted principal. Whichever arrived first warmed the cache for both:
  a deny-all caller was served the unrestricted schema, disclosing every
  restricted attribute name, or an unrestricted caller was served an empty
  one and every query failed. The key is now a hash of the whole policy.
- **The effective-permissions view reports what is enforced.** Enforcement
  ignores the merged field-permission map when the account holds admin, and
  ignores it entirely when a role is unresolved, so a reviewer could read
  `salary: none` off an account with unrestricted field access. The view
  gains `field_acl_bypassed` and `denied_all`, both derived by the same
  function the request path uses (`uow.AccessFromPermissions`), and the
  console shows what applies instead of the map.
- **`FLEXITYPE_TIMEZONE` reaches rule evaluation.** `Service.Interactors`
  stamped the zone onto a context it then discarded, and the HTTP middleware
  never stamped it at all — an interactor set carries no context of its own,
  so every `today`/`now` rule and dynamic default resolved in UTC. The API
  stamps it in middleware; the background loops stamp it too; and embedders
  pass their context through the new `Service.Context` first.

### Fixed — Aggregates: reserved words, non-numeric sources, integer precision and a guard that skipped half its rule

Five defects introduced with the aggregate feature, all of which produced a
plausible number rather than an error.

- **The five aggregate names are no longer reserved words.** `count`, `sum`,
  `min`, `max` and `avg` are aggregates only when a `(` follows them, so an
  attribute named `count` still reads bare in `count * 2`. A stored formula is
  rehydrated without re-validation, so such a formula survived in the database
  and then failed twice: the materializer skipped it silently and froze the
  last value, and the validation sweep — which walks every computed attribute
  in the hierarchy — failed the create or update of UNRELATED attributes with
  an "invalid formula" the caller never wrote. The sweep now skips a stored
  formula it cannot parse, and the materializer reports it through the
  background-error observer instead of swallowing it.
- **`count()` folds members, not numbers.** The numeric inputs hold only the
  values the coercion handles, so `count(tags)` over multi-valued strings
  answered **0** for every entity while the attribute held values — queryable
  in FQL, exported and counted toward completeness. `sum`, `avg`, `min` and
  `max` do fold numbers, so a non-numeric source for those is now refused at
  definition time rather than materializing nothing.
- **An `integer` target is evaluated exactly.** The exact evaluator was wired
  for `decimal` only, so `sum{9007199254740993, 1}` materialized
  `9007199254740992` — wrong by two, with no error and no clear. A genuine
  overflow still clears the value.
- **The structural guard evaluates its two halves independently.** Setting
  `multi_valued` and `localizable` in one request passed the guard entirely,
  because the `continue` that relaxes the multi-valued rule for an aggregate
  reader short-circuited the localizable/scopable refusal too. The
  materializer then folded only the base-scope members: a subtotal presented
  as the total.
- **The reverse guard runs on create.** Writing `total = line_total * 2`
  before `line_total` exists is accepted, so creating `line_total` afterwards
  as multi-valued, localizable or scopable used to skip the guard and leave
  the formula permanently undefined.

`pkg/formula` gains `Members`, `EvalWithMembers`, `EvalRatWithMembers` and
`NumericRefs`. The existing `Eval` and `EvalRat` are unchanged.

### Fixed — A publish that ends mid-flight no longer strands the change-set

A publish claims the set (state `publishing`) before it applies the
mutations, and the release on failure used the caller's context. The
commonest reason a publish fails is that this very context ended — a client
timeout, a load-balancer idle timeout, a pod eviction — so the release failed
too. `Reject` refuses `publishing`, `Publish` refused it, `AddMutation`
refuses it, and the scheduler selected only `approved`: no API could move the
set, its mutations unapplied, and recovery needed a manual `UPDATE` against
`flexitype_changeset`.

- **The release runs on a detached context** (`context.WithoutCancel` plus a
  short deadline), so a cancelled request still hands the claim back.
- **A stale claim is reclaimable.** Once a claim is older than
  `changeset.PublishClaimTTL` (15 minutes), the set is publishable again: the
  scheduler retries it, and so does an explicit publish. The mutations are
  declarative, so a retry reaches the same state whether or not the stranded
  publish committed its values.
- **A reclaim that fails releases to `approved`**, never back into
  `publishing`, so a set cannot be parked for another TTL.
- **Rejecting a publishing set stays refused**, because a stranded publish
  may have committed its values and a rejected set would then report
  untouched data for changes that are live. The refusal now names the reclaim
  path and the time it becomes possible.

The scheduler finds stranded claims through a new optional store interface,
`changeset.ClaimReclaimer`. Both built-in stores implement it. An embedder's
own store still satisfies `changeset.Store` unchanged; without the interface
a stranded set is recovered by publishing it again.

### Fixed — A purge deletes every matching row, in the canonical order, and keeps blobs another entity still uses

A right-to-erasure purge could return a success receipt with the data still
present. The chunk key was the `ctid`, and a `ctid` is not stable across an
`UPDATE`: when a concurrent transaction updated a matching row and committed,
Postgres re-checked the qualification against the new tuple version, whose
`ctid` was not in the chunk's set, so the row was skipped. A chunk that
removed nothing was read as "done". Measured — one row, one concurrent
committed `UPDATE` — the purge reported 0 rows deleted and left the row in
place.

Three changes close it:

- **The chunk key is the primary key.** An `id` survives an `UPDATE`.
- **The chunk is ordered by `entity_id, id` and locked `FOR UPDATE` in an
  ordered CTE.** Every value write refreshes a shared entity-summary row, so
  a purge that took rows in physical order deadlocked against a
  canonical-order batch write. An `ORDER BY` alone was not enough: it decides
  only *which* rows a chunk takes, and the `DELETE` still locked them in its
  own scan order (measured — still deadlocking after the `ORDER BY`). The
  `LockRows` node sits above the `Sort`, so the CTE takes the row locks in
  entity order. `application/value/lockorder.go` now states that a `DELETE`
  is a writer under the same rule.
- **Completion is decided by a count, not by an empty chunk.** A run of
  chunks that remove nothing while rows still match is reported as an error.
  Erasure is the operation least able to tolerate a false success.

Two related media fixes ship with it:

- **Erasure counts references before deleting a blob.** Adoption is the
  sanctioned way to reuse an object key, so a shared key is the expected
  shape. The erasure GC deleted unconditionally while the ordinary write path
  counted references, so purging one entity destroyed a different entity's
  live document and left it a media value whose bytes 404. Keys kept for
  another value are listed on the purge report as `retained_blob_keys`.
- **The owner of an object key is decided deterministically.** The owning
  value decides which attribute's field ACL governs adoption and download.
  `ORDER BY created_at` left a tie to physical row order, and two values
  written in one batch share a timestamp. The `id` now breaks the tie, in
  both backends.

### Fixed — The release no longer tags modules `go get` cannot resolve

`cmd/flexitype` and `infrastructure/gcppubsub` carry a `replace` of the core
module and require it at the zero pseudo-version. A published module's
`replace` is **ignored by consumers**, so the `cmd/flexitype/vX.Y.Z` and
`infrastructure/gcppubsub/vX.Y.Z` tags the release workflow cut could never
be resolved by `go get` — worse than no tag, because the module looks
available. The workflow now tags only `client`, which has no first-party
`replace`, and a test holds the workflow's loop and that rule together.

Two things were measured rather than assumed while doing this:

- **The core module's zip already excludes the nested directories.** Built
  with `golang.org/x/mod/zip` — the library the toolchain uses — it contains
  **0 files** from `cmd/flexitype/`, `infrastructure/gcppubsub/` and
  `client/`, because each holds a `go.mod`. The "same package path in two
  modules" hazard `docs/api-stability.md` warned about is therefore already
  gone, which is the part of the staged release that needed proving.
- **`go.work` is not a substitute for the `replace`.** Removing it breaks the
  build inside the workspace too (`invalid version: unknown revision
  000000000000`): the module graph loads before workspace substitution
  applies, so a require naming an unresolvable version fails first.

`docs/api-stability.md` now carries the exact two-release sequence that makes
both modules go-gettable, and why it takes two.

### Added — A Roles page in the admin console

Roles were API-only. The console now has `/roles`: create and replace a role,
edit its scopes and per-attribute permissions, and see **which accounts hold
it** — which is the fact the delete guard turns on, so it is in front of the
operator before the 409 rather than after it.

Opening an account shows what it can **actually** do: its own scopes unioned
with its roles', and the merged per-attribute levels, resolved through the same
`serviceaccount.Resolve` the enforcement path uses. An account naming a role
that no longer exists is called out in red, because that principal is denied
every attribute until it is repaired.

The editor states the two rules that are otherwise only in the API's error
messages: a role cannot grant `admin`, and a write replaces the whole role.

### Added — Computed aggregates: `sum`, `count`, `avg`, `min`, `max`

A formula could not read a multi-valued attribute at all. Evaluation carried
one value per name, so a multi-valued source collapsed to whichever member the
repository returned last — adding a member changed the answer with no change
to the schema or the formula — and the definition is now refused outright.
Refusing was right; leaving no way to express the aggregate was the gap.

```
{"kind": "formula", "formula": "sum(line_totals)"}
```

- **A name carries every value the entity holds for it.** A bare name must
  resolve to exactly one; an aggregate folds the whole list. So
  `line_totals * 2` is still refused and `sum(line_totals)` is not, and the
  rule is enforced at definition time rather than producing a number later.
- **The reverse guard follows the same rule.** An attribute a formula reads
  bare cannot be made multi-valued; one that is only ever aggregated can,
  because the aggregate already asked for every member.
- **`count` of an absent attribute is 0; `sum`, `avg`, `min` and `max` are
  undefined.** Counting nothing has one reading. A total over no data is
  unknown rather than nought, and an undefined formula clears the value.
- **Exact for decimals.** Aggregates evaluate in rational arithmetic on a
  `decimal` target, so `sum` of `0.1` and `0.2` stores `0.3`.
- Localizable and scopable sources stay refused, aggregated or not:
  evaluation reads the base scope, and folding values that mean different
  things per locale is not an aggregate anyone asked for.

Rollups **across relationships** remain unsupported and are documented as a
gap rather than approximated. New: [docs/computed.md](docs/computed.md).

### Documentation — GraphQL introspection and the field ACL

The GraphQL schema is built from the caller's READABLE attribute set and
cached per `(tenant | permission profile)`, so introspection already hides an
unreadable attribute's existence exactly as the grid, FQL and export do. That
was not written down anywhere, and #422 reported the opposite from a grep for
`CanRead` inside `application/gql` — the filtering happens one layer down, in
the interactors the schema builder reads through.

It is now stated in the field-permission surface table, and pinned by a test
that introspects `__type` as a restricted principal and as an unrestricted
one in turn.

### Fixed — Residuals, and four documents that contradicted the code

- **Restoring a pre-narrowing revision was impossible** (#420). `Restore`
  replays with the captured (parent) type; the write path saw
  `supplied != anchor`, tested "is the supplied type a descendant" and
  refused with "the entity is already anchored to an unrelated type" — of the
  entity's own **parent**. A supplied ANCESTOR is now satisfied where the
  entity already is: the write lands under the existing, narrower anchor and
  nothing moves back to the parent. An unrelated type is still refused.
- **CSV round-trip corrupted JSON arrays of `{"value": …}` objects** (#420).
  The multi-value cell format was an untagged JSON array, indistinguishable
  from a legitimate payload, so exporting and re-importing
  `[{"value":{"x":1}},{"value":{"y":2}}]` in a json column stored `{"y":2}`
  with zero errors reported. The format is tagged — `{"values":[…]}` — and the
  untagged form is still accepted on import for non-json columns, so an older
  export still loads.
- **A computed value whose formula became undefined was never cleared**
  (#420). The rebuild used the non-clearing variant, so after an edit
  introduced a division by zero the pre-edit value survived indefinitely:
  queryable in FQL, present in exports, counted toward completeness, with no
  formula that produces it. The rebuild clears it now — the source
  fingerprint added alongside makes that safe, because a clear from
  half-written inputs is followed by a source change the fingerprint sees.
- **A known route with an unsupported method answered 404** (#420), putting
  "endpoint absent" and "endpoint present, wrong verb" behind one status —
  the ambiguity the JSON-404 change existed to remove. It answers **405 with
  an `Allow` header**, built by walking the router.
- **`/api`, `//api/…` and `/API/…` answered 200 with the app shell** (#420).
  The prefix test ran on the raw path, and `//api/…` is exactly what naive
  base-URL joining produces. The path is normalised and case-folded first.
- **A GraphQL failure raised before execution was masked without telling the
  error observer** (#420), whose godoc promises it reports every error masked
  on its way out — so an operator saw "internal error" with nothing naming the
  cause. Pre-execution failures go through the same sanitizer and the same
  observer. The relationship-depth error is a validation error, so it reads
  the same as it does on the federation path.
- **`Schema().Template` dropped the bundle its godoc promises** (#420), making
  it identical to `Templates()`. The server always sent it; the client type
  had no field for it.
- **`EntityAnchor` had no tiebreaker** (#420). Rows written in one batch share
  a `created_at`, so `ORDER BY created_at LIMIT 1` left the anchor undefined
  and an entity could flap between anchors, rewriting every row on each write.
  Both backends break the tie on id.

Four documents asserted the opposite of the code, and now do not: the webhook
`RotateSecret` godoc promised a grace window removed eleven lines above it (it
is a hard cutover — update receivers first); `Service.Dispatcher()` warned that
registration is unsynchronised, which the copy-on-write change made false; the
rate-limit ceilings are documented as **per process**, which matters in the
release that added `FLEXITYPE_RUN_*` so operators run several replicas; and
`FLEXITYPE_REQUIRE_AUTH` (fixed earlier in this release) no longer disables
authentication.

### Fixed — Schema-change guards that read the stored data wrongly

- **`multi_valued → single` was refused for legal scoped data** (#420). The
  guard grouped by entity alone, so a localizable attribute holding one value
  per locale read as "more than one value" — and the only way through the
  migration was deleting real data the new schema expresses perfectly. It
  groups by `(entity, locale, channel)` in both backends.
- **The unit family was structural but unguarded** (#420). Flipping
  `mass → length` with stored data succeeded, after which every stored base
  magnitude read in the new family's base unit (2000 g as 2000 m) and the
  stored display unit was not a member of the new family — so those rows could
  never be rewritten through the API either. It is refused while values exist.
- **The in-memory backend counted every non-text value as a duplicate**
  (#420). It keyed on `Value.Text()`, which is `""` for integer, float, bool,
  date, json, media and quantity, so a `unique false → true` flip was refused
  for three distinct integers — while Postgres allowed it. The backends
  disagreed on both the data and the semantics; the in-memory one now keys on
  the value's own rendering and counts distinct entities, as Postgres does.
- **Restoring an archived attribute re-introduced a sibling name collision**
  (#420). `Restore` ran no uniqueness re-check, and `Create`'s guard uses
  `GetByInternalName`, which excludes archived rows — so archiving
  `release_code` on one subtype, creating it on a sibling and restoring the
  first left two live attributes with one name, resolved by list order.
  Restore runs the same hierarchy sweep Create does.
- **Archiving a parent type did not stop writes under live subtypes** (#420).
  The guard checked only the anchor's own flag while the FQL binder skips an
  archived type anywhere in the chain, so writes succeeded and were then
  unqueryable — the original failure mode, reached by archiving the parent
  instead of the leaf. The write path walks the chain.
- **`one_of` quantity members in a dependency effect were never rebased**
  (#418). The effect loop rebased `min_value` and `max_value` but had no
  `one_of` arm, so a caller's base magnitude was compared against an
  unconverted member and the **exact allowed quantity** was refused, with the
  error blaming the writer for a rule the schema author wrote.
- **Facets resolved a type's schema through a fixed 500-row page** (#418) —
  the seventh call site, left behind when six were converted and the cap
  raised to 1000, which only widened the window in which it truncated.
- **The facet/grid sweep and the GraphQL connection paged on a mutable sort
  column** (#418). An entity written mid-sweep jumped ahead of the cursor and
  was omitted from the facet counts, which then disagreed with the grid beside
  them. Both use the stable listing; the GraphQL cursor now encodes the key
  that listing orders on, which is what makes the next page correct.

### Fixed — Go client: three methods returned silently wrong data

Six defects (#419), three of them silent-wrong-answer bugs on methods added to
close earlier gaps.

- **`Events().List` returned all-zero events.** The client type was flat
  (`feed_seq`, `id`, `type`, …); the server sends
  `{"seq":N,"envelope":{…}}`. **No key overlapped**, so decoding produced the
  right *number* of events with every field zero and no error, and a consumer
  dispatching on `Type` saw `""` for ever. `FeedEvent` now decodes the nested
  shape and keeps a flat surface.
- **`Events().ListPage` could never succeed.** `NextCursor` was declared a
  string while the server emits a number, and `next_cursor` is always present
  — so the method failed on every call, including against an empty feed. It is
  an `int64`, and `after` is an `int64` too, so the cursor a page returns is
  usable as the next page's argument.
- **`Values().List(ChangeSetID:)` returned live values.** The option sent
  `changeset` to the paginated `/values` endpoint, which does not read it; the
  overlay exists only on `GET /entities/{type}/{entity}/values`. A reviewer
  previewing a change-set was shown the **pre-change** values as the preview
  and could approve a diff that was never displayed. The option is gone,
  replaced by `Entities().ValuesPreview`, and the server now **refuses**
  `?changeset=` on `/values` rather than ignoring it.
- **`SavedViews().Update` was neither a full replace nor sparse.** A nil
  `Columns` marshalled to `null`, which the server's sparse decoder reads as
  "absent", so a rename cleared `query` and `sort` while leaving `columns`
  alone. Columns are always transmitted, as `[]` when nil, and the godoc
  points renames at `Patch`.
- **`RevisionValue` dropped `locale`, `channel` and `typed`.** `AsOf` returned
  a localized entity as N rows with identical `internal_name` and no way to
  tell them apart, and rendered a quantity as the lossy display string — the
  typed form exists on the server precisely because the display form is lossy.

### Fixed — Saved views: a concurrent patch no longer discards a field

`Patch` did `Get`, merged, then called `Update`, which performed a second
unguarded `Get` and a blind write — in the same release that added optimistic
locking to change-sets. Two concurrent patches, one setting the sort and one
renaming, each wrote the other's field back as it was: the "one client
silently clears what another set" outcome the sparse decoder was added to
remove, moved from an omitted field to a concurrent write.

Saved views now carry a `version` (migration `000029`), and the store's
`Update` is a compare-and-swap that answers **409** on a stale write. `Patch`
compares against the version it merged against.

### Fixed — Event retention and the bulk redrive

- **Dead deliveries pinned their envelopes for ever** (#405). The envelope
  prune keeps anything a `dead` delivery references — correct, since the dead
  letter is the evidence an operator needs in order to redrive it — but
  nothing else ever deleted a dead row. One decommissioned webhook endpoint
  therefore held its envelopes indefinitely, so `FLEXITYPE_EVENT_RETENTION`
  stopped bounding the outbox (described in migration 000025 as "the largest
  table in an event-heavy deployment") or the events feed, and the hourly
  prune scanned a table that could never shrink. Nothing surfaced it: the
  deployment looked healthy until the table did. Dead letters now have their
  own bound — `FLEXITYPE_DEAD_LETTER_RETENTION` /
  `flexitype.WithDeadLetterRetention`, default **30 days**, far longer than
  the event retention so a dead letter outlives the events it references. They
  are pruned before the envelopes in the same pass, so an expired one frees
  its envelope immediately.
- **The bulk redrive was one unbounded UPDATE, all due at the same instant**
  (#406) — the two shapes the same release had just fixed in the pruner. After
  a day-long outage, `POST /api/v1/webhook-deliveries/redeliver-dead` opened
  one transaction that locked and rewrote every dead row, blocking the
  worker's `FOR UPDATE SKIP LOCKED` claims and the delivery-stats scrape for
  its duration; it then handed the worker the entire backlog with an identical
  `next_attempt_at`, pointed at the endpoint that had just come back. It now
  redrives in bounded batches and spreads `next_attempt_at` across a
  five-minute ramp, so the recovered consumer sees the backlog as a rate
  rather than a spike.

### Fixed — Write-path correctness

Four places where a write did something other than what the API described.

- **A failed compare-and-swap left the data written** (#415). `Publish`
  applied the mutations and only then performed the version-guarded `Update`.
  Once that call could fail, any concurrent touch of the set — a reviewer
  rejecting it, a second publish, the scheduler tick — committed the values and
  left the record saying something else. Through `PublishDue` it compounded:
  the set stayed `approved` with `publish_at` in the past, so every tick
  re-applied the same mutations over whatever had been written in between, and
  a **rejected** set could have its contents applied on a timer. The claim is
  now taken **first** (new `publishing` state, version bumped), the mutations
  applied second, the claim finalised third. A constraint failure hands the
  claim back; a set left `publishing` means a publish began and did not finish,
  and the scheduler does not pick it up.
- **Context-key rules and the tenant-local day were ignored on the write
  path** (#403). `EffectiveSchema` and `Completeness` evaluated rules with the
  caller's context values and the tenant-local day; `checkDependencies`
  evaluated the same rules with neither, and a condition naming a
  `ContextKey` short-circuits to "no match" when the key is absent. So a write
  was accepted that the API had just reported as forbidden — the worst
  combination for a validation feature, because it looks configured and
  tested. The write path now uses the same resolver and the same clock, and
  the context-free `ResolveEffective`/`MatchesAny` are **removed**, so the
  paths cannot drift again.
- **The computed rebuild gated on wall clocks** (#407). It compared the
  entity's `last_updated_at` — stamped when a write *began* — against the
  rebuilding process's own clock, on a different machine. A write that began
  before the rebuild started and committed after it listed the entity was
  invisible to the check, so the rebuild wrote a computed value from
  pre-write inputs and left it stale until that entity was written again.
  Clock skew widened the window. The rebuild now compares a **fingerprint of
  the source values** before and after it writes, and recomputes if they
  moved: no synchronised time, and no assumption about which replica ran.
- **Formulas silently collapsed multi-valued and scoped sources** (#421).
  Evaluation carries one number per source name, so a multi-valued source
  became whichever member came back last and a scoped value was skipped.
  Adding a member changed the answer with nothing to explain it, while the
  computed attribute stayed populated, queryable and counted toward
  completeness. Such a formula is now **refused at write time**, in both
  directions: a formula cannot read a multi-valued, localizable or scopable
  attribute, and an attribute a formula reads cannot become one. An aggregate
  over many members is a different feature, and is refused rather than
  approximated — the same call this project made for rollups.

### Fixed — The deployment trust boundary

Three ways a deployment served more than its operator believed.

- **Library mode served the whole API to anonymous callers** (#414). The
  fail-closed authentication default was added to `internal/config`, which
  governs only the standalone binary. `APIConfig.Accounts == nil` still
  stamped an admin principal on every request, with no boot-time refusal and
  not even the warning the binary prints — so an embedder who mounted the
  handler the way the embedding guide documents served everything, including
  the irreversible `POST /admin/purge`, unauthenticated. That state now needs
  an explicit `AllowAnonymous`: `NewAPIHandler` returns an error without it,
  and `APIHandler` panics, because it is a composition-time misconfiguration
  rather than a per-request one.
- **Failed authentication was never rate limited** (#416). The per-account and
  per-tenant limiters run *after* `authenticate`, which writes 401 without
  calling `next` — so 200 bad tokens meant 200 credential lookups, each a
  database round trip and a hash, none cached and none counted. An
  unauthenticated caller could exhaust the connection pool with a loop, and
  token brute-force had no throttle at all. A pre-authentication limiter keyed
  on the **client address** now runs first
  (`FLEXITYPE_AUTH_RATE_LIMIT_RPS`, default 20; `APIConfig.AuthRateLimiter`),
  and its rejections count on the existing rate-limit metric. It deliberately
  ignores `X-Forwarded-For`: a header is attacker-supplied, so trusting it
  would let one client spread its attempts across unlimited keys.
- **`FLEXITYPE_DB_PARAMS` could turn off TLS or redirect the connection**
  (#402). The params are appended verbatim to the connection string and libpq
  resolves duplicate keywords **last-wins**, while the guard re-parsed only
  when `FLEXITYPE_DB_URL` was set — so `DB_PARAMS="sslmode=disable"` shipped
  credentials in the clear with `sslmode=require` still in the manifest, and
  `host=`/`hostaddr=` sent them to another server. The guard now reads the
  **rendered DSN**, so it evaluates what libpq will use, including `hostaddr`,
  which bypasses name resolution entirely.

Also: **`FLEXITYPE_REQUIRE_AUTH=false` was a second, undocumented way to boot
unauthenticated** (#420) — it skipped the account-source check, so a manifest
carried over from before the fail-closed default kept booting open while the
warning named `FLEXITYPE_DEV_INSECURE`, which nobody had set. It no longer
disables anything, and a deployment that set it with no account source is
refused and told which variable actually expresses that intent.

### Fixed — Concurrent migrations through a transaction-mode pooler

The migration runner took a **session-scoped** advisory lock and held it
across the per-migration transactions. `docs/configuration.md` states as a
supported contract that no lock outlives the transaction that took it, and
names this exact hazard.

Measured through PgBouncer in transaction mode with a contended pool, six
concurrent `Migrate()` calls against an empty database — the rolling-deploy
shape — produced **five failures** on raw DDL collisions (`relation
"flexitype_type_definition" already exists`, and unique violations on a
catalogue index), because the lock was taken on one pooled backend while the
transactions it was meant to protect ran on others. One run then left the lock
held on an idle backend, where every subsequent migration blocked forever with
no diagnostic and no application-level recovery.

Mutual exclusion no longer lives in session state:

- A **lease row** (`flexitype_schema_lock`) replaces the session lock. It is
  visible to every backend, refreshed between statements, and **expires**, so
  a runner that dies frees it instead of stranding it. A runner that loses its
  lease stops rather than applying DDL alongside the new holder.
- Each transactional migration **claims its version row before the DDL, in the
  same transaction**. A second runner's insert blocks on the primary key until
  the first commits, then finds the version taken and skips — so the runners
  serialize on the database, for exactly as long as an apply takes, with no
  session state at all.
- `CREATE TABLE IF NOT EXISTS` is not atomic against a concurrent identical
  statement: both sessions pass the existence check and the loser fails on a
  catalogue index. That is what broke *before* any lock could be taken, since
  it is the step that creates the table the lock lives in. Concurrent creation
  is now treated as success.

CI runs the concurrent case on a contended pool (`DEFAULT_POOL_SIZE=2`, six
runners). The existing PgBouncer leg could not catch this: it is serial and
the pool is idle, so consecutive statements land on the same backend and a
session lock appears to work.

`docs/upgrades.md` and `docs/configuration.md` stated opposite contracts; both
now describe the lease. Closes #400.
### Fixed — Entity-summary lock order, and the cost of an unbounded purge

- **Change-set publish and CSV import still deadlocked** (#412). The statement
  trigger sorts keys *within one statement*, and `SetBatch` sorted its own
  items — but every other multi-entity path issues one INSERT per value in
  caller order, so two of them over the same entities in opposite order
  deadlocked on the summary rows even though their value rows were disjoint:
  40 rounds out of 40 in the reported reproduction. `ApplyMutations`,
  `importTransactional` and `writeChunk` now apply the same canonical order.
  `ApplySnapshot` needs none — a snapshot restores one entity, so it touches
  one summary row.
- **The sort key itself was wrong** (#412). `SetBatch` ordered on the
  caller-supplied `TypeDefinitionID`, which is optional, so a batch that omits
  it and one that supplies it sorted the same mixed-type entities differently
  and could still invert their lock order. The key is now **the entity id
  alone**: an entity has exactly one anchor type, so it has exactly one
  summary row, and the entity id is a total order over the rows a transaction
  takes. The ordering lives in one place (`application/value/lockorder.go`)
  rather than in each caller's loop.
- **A tenant purge spilled to temp disk** (#417). The `FOR EACH STATEMENT`
  trigger uses `REFERENCING OLD TABLE`, so an unbounded `DELETE` materialises
  every removed row into a tuplestore: measured at 300k rows with
  `work_mem=4MB`, **17.2x slower** than with the triggers dropped, and ~42MB
  of temp blocks that did not exist before. At the 10^8 value rows migration
  000022 cites as the target scale, that is on the order of 14GB of temp files
  inside a single uninterruptible statement — on erasure, the operation least
  able to tolerate a partial failure. Both purges now delete in bounded
  chunks inside the caller's transaction, so the purge stays atomic while each
  transition table stays proportional to the chunk.

### Fixed — Erasure and field-ACL residue

Five surfaces where a value survived an operation that reported success, or
was readable by a principal every other surface hides it from.

- **Media object keys could be laundered past the field ACL** (#411).
  Adoption authorized a media write on tenant ownership of the key alone,
  while the download check granted the bytes if *any* attribute referencing
  the key was readable. A principal restricted from `passport_scan` needed
  only write access to some other media attribute: adopt the restricted key
  into `avatar`, then download it. Object keys are not secret — they leak into
  value payloads, exports and revision snapshots — so nothing had to be
  guessed. The bytes now belong to the attribute that first referenced them,
  and **that attribute governs both adoption and download**. A caller who may
  not read it gets the same error as for a key that does not exist.
- **Narrowing an entity's anchor orphaned its revisions** (#413). Value rows
  moved to the subtype; revision rows, keyed on the type, did not. A
  pre-narrowing revision became invisible under the new anchor, and
  `PurgeEntity` — which purges under the new anchor — reported
  `revisions_purged: 0` and success while a complete snapshot of the entity's
  values, personal data included, stayed readable through `Revisions().Get`.
  **Revisions are now keyed on (tenant, entity)**, so no type change can
  separate an entity from its own history. Migration `000028` adds the
  matching index. The type in the route is still validated, so a bogus type
  answers with an error rather than another type's history.
- **Change-set mutations had no field ACL** (#418). A mutation embeds the
  value verbatim, so a principal with `salary: none` read the salary from
  another user's staged pay review. `Get` and `List` now mask the value and
  keep the skeleton, as the feed and activity log do; `AddMutation` enforces
  write access, because staging a write is writing, only later.
- **Erasure left values in change-set mutations** (#418). The residual
  erasers covered the outbox and the activity log; `flexitype_changeset.mutations`
  is JSONB embedding the value, and a draft or rejected set is never pruned,
  so the copy was indefinite. A change-set eraser is registered in both
  backends. The mutation skeleton survives — deleting it would silently change
  what the set does when published.
- **Reference-counted blob GC never collected a shared key** (#418). The count
  included archived rows, so with N references each archival saw the other
  N-1 and declined: once every reference was archived the bytes stayed in
  object storage with nothing live pointing at them, and a "delete my file"
  request through the soft-delete path left the file. Only live rows count.

### Fixed — Roles: a delete no longer hands out the attributes it was hiding

Six defects in the role feature, five of them ways a permission change did the
opposite of what the operator intended.

- **Deleting a role failed OPEN** (#399). A role that resolves to nothing
  contributes no field permissions, and an empty permission map read as
  "unrestricted" — so retiring a role, or renaming it by create-then-delete,
  converted every account restricted *only* by that role into one with full
  field access. There was no error, no log line and no change to the account
  rows, which still listed the role name, so the listing looked unchanged
  while the effective permission had inverted. Two fixes: `DELETE
  /api/v1/roles/{name}` is refused with **409** while any account still names
  the role, and a principal carrying a role that does not resolve is now
  **denied every attribute** rather than granted every one — covering a row
  edited directly in the database.
- **A role could grant `admin`** (#401), which is a cross-tenant privilege
  that also short-circuits the whole field ACL, so it voided the account's own
  `field_permissions` and conferred the control plane — while the account row
  still read `scopes: ["read"]` with a restriction. `admin` is now refused in
  a role's scope set, and dropped at resolution if a stored row carries it.
- **A role edit never evicted the auth cache** (#404), so the change that most
  needs immediate effect — removing a grant during an incident — was deferred
  by up to `FLEXITYPE_AUTH_CACHE_TTL` on every replica, with the API having
  already confirmed it. A role write or delete now evicts every cached
  authentication for the tenant. **Tenant deactivation does too**, which it
  never did (#418): a suspended tenant kept working for the TTL.
- **`PUT /api/v1/roles` returned a fabricated id and `created_at`** when it
  updated an existing role (#408) — the upsert deliberately preserves both, so
  the response described a row that did not exist and an idempotent
  provisioning script recorded a different non-existent id on every re-run.
  The persisted values are returned.
- **Roles were resolved before the credential was verified** (#409), so a
  garbage secret for a known account id cost two unbounded database round
  trips instead of one, and a known id with roles answered measurably slower
  than an unknown one — an account-id oracle with attacker-controlled work
  amplification. The secret is verified first.

### Added — An effective-permissions view

`GET /api/v1/service-accounts/{id}/effective` (admin scope; `AdminService.EffectiveAccount`
in the Go client) reports what a principal can **actually** do: its own scopes
unioned with its roles', the merged per-attribute permissions, and any
`unresolved_roles`. The list endpoints report what is stored, which is what an
operator edits; answering "is this account safe" previously meant fetching
every role it names and unioning by hand.

The merge now lives in one function, `serviceaccount.Resolve`, which both
authentication and this view call, so the report cannot say one thing while
enforcement does another.

### Changed — Role storage and deployment manifests

- Migration `000027` drops `idx_flexitype_role_tenant`, which duplicated the
  index behind `UNIQUE (tenant_id, name)` byte for byte (#409), and adds a GIN
  index on `flexitype_service_account.roles` for the containment test the
  delete guard runs.
- `deploy/kubernetes/20-worker.yaml` gains a headless Service (#409). The
  ServiceMonitor selects Services and only the API tier had one, so no
  `up{...component="worker"}` series existed — the worker tier being scaled to
  zero or never applied was invisible to monitoring, while the outbox-lag
  alert's remediation text asked operators to check exactly that.

### Added — GraphQL: an opt-in Apollo Federation subgraph

The GraphQL endpoint was a standalone schema with no `_service`, no
`_entities` and no `@key`. A gateway cannot compose a subgraph without them,
so an adopter already running a federated graph could not add this service at
all — and the shape that suits an attribute service best, where dynamic
attributes appear as fields on a type another subgraph owns, was closed to
them. The fallback was to call the REST API from their own graph layer and
merge results by hand.

`FLEXITYPE_FEATURE_GRAPHQL_FEDERATION=true`, or
`flexitype.WithGraphQLFederation()`, now serves the subgraph contract:

- `_service { sdl }` returns the subgraph SDL with `@key(fields: "entityId")`
  on every entity type. `entityId` is the same opaque id every other surface
  uses, so the gateway joins on an id your own system already owns.
- `_entities(representations: [_Any!]!)` resolves entities from another
  subgraph's representations, through the same batched read path the
  connections use. The answer preserves the representation order — position is
  the only thing that ties a result to its representation — and a repeated id
  is read once and placed at both positions.
- The field ACL applies. The SDL a caller reads carries only the types and
  attributes that caller may read, and `_entities` resolves the same set.
- A batch is capped at 500 representations: it is a single list argument, so
  it escapes the field-count cost guard entirely.
- An id this service holds no values for resolves to an object with null
  fields, not an error. flexitype does not own entity existence.

It is off by default. A federated schema carries three fields no standalone
client asks for, and `_entities` is a batch read a non-federated deployment has
no reason to expose.

Documentation: [docs/federation.md](docs/federation.md). Closes #327.

### Fixed — GraphQL: a domain error from a resolver read as "internal error"

graphql-go wraps every error a resolver returns in `*gqlerrors.Error` to
attach the field path. That type carries its cause in an `OriginalError` field
and has no `Unwrap` method, so the sanitizer's `errors.As` stopped at the
wrapper and masked the error. Every domain error a resolver raised therefore
reached the client as `internal error`: a not-found read as a server fault,
and a validation message the caller needed in order to fix its own request was
replaced by nothing. The sanitizer now peels the wrapper first, so GraphQL
returns the same client-facing messages REST does. Infrastructure errors are
still masked.

### Added — Roles: one permission set, many accounts

Field permissions were stored per service account with no indirection. A
deployment fronting 500 operators across 5 permission profiles needed 500
accounts, each carrying its own copy of the per-attribute map. Restricting one
more attribute meant hundreds of writes that no transaction spanned; the
permission applied inconsistently while the rollout ran; and "who can read
this attribute" could only be answered by reading every account and diffing
maps.

A **role** is a named permission set inside one tenant, owning a scope set and
a per-attribute permission map. Accounts hold role names; nothing is copied
onto them.

- `PUT /api/v1/roles`, `GET /api/v1/roles?tenant_name=…`,
  `DELETE /api/v1/roles/{name}?tenant_name=…`, and
  `PUT /api/v1/service-accounts/{id}/roles`. All need the `admin` scope, and
  all are in the OpenAPI document and the Go client
  (`AdminService.UpsertRole`, `ListRoles`, `DeleteRole`, `AssignRoles`).
- `POST /api/v1/service-accounts` accepts `roles` and `field_permissions`.
  `scopes` becomes optional when a role is given: an account whose whole
  permission set comes from its roles needs no scope of its own. An account
  with neither a scope nor a role is refused — the credential would grant
  nothing.
- The merge happens at **authentication**, so a change to a role reaches every
  account holding it as soon as the auth-cache entry expires. Scopes union;
  field permissions take the most permissive level any role grants; the
  account's own entry wins over every role. `write` outranks `read`, which
  outranks `none`, and an unrecognised level ranks below `none` so a typo
  denies.
- An assignment naming a role the tenant does not have is a 404. A missing
  role resolves to nothing, so a typo would otherwise be indistinguishable
  from a role that grants nothing.
- `PUT /service-accounts/{id}/roles` evicts the account's auth-cache entry, so
  a removal applies at once rather than at the end of the TTL. A change to a
  role still waits for the TTL: the accounts holding it are not known at write
  time.
- Migration `000026` adds `flexitype_role` and the `roles` /
  `field_permissions` columns on `flexitype_service_account`. Both columns
  default to empty, so an existing account behaves exactly as before.

Rules and examples: [docs/design/identity.md](docs/design/identity.md#roles).
Closes #326.

### Added — Go client: opt-in retrying, and the throttle hint it dropped

`Client.do` made exactly one attempt, and `decodeError` never read
`resp.Header`, so the `Retry-After` the server sets on every throttle was
thrown away. Callers saw `RATE_LIMITED` with no wait hint and either failed
outright or invented their own backoff — while the hint they needed was on
the wire.

- `APIError.RetryAfter` carries the parsed hint (both RFC 9110 forms: a delay
  in seconds and an HTTP date). A date in the past gives zero, not a negative
  wait.
- `WithRetry(RetryPolicy)` enables retrying; `DefaultRetryPolicy()` is three
  attempts on 429/502/503/504 with exponential backoff from 200ms to 5s and
  ±20% jitter. A server-supplied `Retry-After` always beats the computed
  delay, and the loop stops when the caller's context ends.
- **Only idempotent methods are retried** — GET, HEAD, PUT, DELETE. A POST is
  never replayed, whatever the policy says: it may have been applied before
  the connection broke.

Retrying stays off by default. The paginating `All(...)` iterators are the
case that most wants it: they issue many requests in tight succession, which
is the exact shape a per-account token bucket targets, so a bulk walk used to
abort part-way with no resumption point.

### Changed — Go client: nil on error, and a base URL that must be usable

`New` rejected only the empty string and a `url.Parse` failure, which almost
nothing triggers — `url.Parse("localhost:8080")` succeeds, reading
`localhost` as the scheme. So `client.New("localhost:8080")`, the most
natural thing to type for a local service, constructed successfully and then
failed every request with "unsupported protocol scheme", pointing an adopter
at their network or their token rather than at one missing `http://`. `New`
now requires an http/https scheme and a host, and says which is missing.

About fifty methods returned a non-nil pointer to a zero value alongside
their error, while a handful returned nil. **They now all return nil.** Code
that checks the error is unaffected; code that nil-checked instead used to
carry on with an empty id and zero timestamps, and now fails where the
mistake is. The convention is stated once in the package doc.

### Added — Go client: `CursorStack` for backward paging

`PageInfo.HasPreviousPage` reports that a previous page exists, but the API
has no `before` cursor and no direction, so an adopter who wired a Back
button found nothing to call — and the field read like a supported
capability. Its godoc now says what it does and does not mean, and
`CursorStack` keeps the visited cursors so a paged view can step back, the
way the admin console already does in its own composable.

### BREAKING — Go client: `Revisions().Diff` takes two revision ids

`Diff(ctx, id)` becomes `Diff(ctx, fromID, toID)`. The endpoint diffs one
revision against another and requires `?to=`; the old signature sent no
query, so **every call returned `VALIDATION: to (revision id) is required`**.
No call could succeed, so no working code depends on the old shape — but the
change is a compile error, and it is called out here rather than buried.

Pass the newer revision as `toID`:

```go
diff, err := c.Revisions().Diff(ctx, oldRev.ID, newRev.ID)
```

Two more methods changed behaviour without changing their signature. Both
could only fail before, so neither can break working code:

- `Entities().AsOf` decoded a bare revision object through the
  `{"items":[...]}` list helper, so it returned an **empty slice and a nil
  error** on every call. Point-in-time reads rendered an empty entity and
  looked like data that was never set. It now decodes the object, and an
  empty timestamp is rejected locally rather than as a server 422. Use the
  new `AsOfRevision` when the revision's id, seq or label is needed too.
- `Admin().ListServiceAccounts` sent `tenant` where the server reads
  `tenant_name`, so the filter was dropped and the call always 422'd. The
  tenant argument is now required and rejected locally when empty.

### Added — Go client: capabilities that had no method

Eight REST capabilities were reachable only over raw `net/http`, including
both right-to-erasure endpoints that `docs/erasure.md` documents. A team that
standardised on the SDK had to hand-roll erasure — the one operation with a
statutory deadline — with its own auth, error decoding and retries.

- `Entities().Purge` and `Client.PurgeTenant` (right to erasure).
- `Values().List` / `.All`, with the `type_definition_id`,
  `attribute_definition_id`, `entity_id` and `include_archived` filters, plus
  the `changeset` draft-preview overlay.
- `RelationshipDefinitions().Update` and `.AttributeSets`.
- `Schema().Template(name)`.
- `Client.RecomputeComputed`.
- `Events().ListPage`, which returns the feed's `next_cursor` — the list
  helper used to discard it, so there was no supported way to ask for the
  second page.

Streaming (`GET /events/stream`) stays out of scope, and the `EventsService`
godoc now says so: server-sent events need a long-lived connection with its
own reconnect policy, and the cursor gives at-least-once delivery across
restarts that a stream alone does not.

`TestClientRouteCoverage` now walks the live router and fails when a route has
no client method, so this cannot recur silently.

### Fixed — Go client: dropped response fields

- `ChangeSet.Name` is populated. The field was declared as `title`, which the
  server never sends, so every change-set read back with a blank label — a
  valid empty string, so it looked like an unnamed change-set rather than a
  bug. `Title` remains as a deprecated mirror of `Name` so existing code
  keeps compiling, and it is removed in the next major version. `Approver`
  and `PublishedAt` are added.
- `SavedView` gains `Sort`, `CreatedAt` and `UpdatedAt`. See the saved-view
  entry below for the data loss the missing `Sort` caused.
- `Facets.Truncated` is populated (and added to `api/openapi.yaml`, so
  generated clients get it too). Without it a partial bucket list is
  indistinguishable from a complete one, so a filter sidebar shows
  "3 materials" where there are 300.

### Fixed — a saved-view PATCH no longer clears the fields it omits

`PATCH /api/v1/saved-views/{id}` decoded into a value struct, so any field the
caller omitted was written back as its zero value. Renaming a view through any
client — the SDK, curl, a generated client — silently cleared the sort order
configured through another, and sort order is part of what makes a saved view
reproducible.

The handler is now sparse: an absent field keeps its stored value, and an
explicit empty value still clears it. In the SDK, `SavedViews().Update` keeps
full-replace semantics (and now carries `Sort`), and the new
`SavedViews().Patch` takes a sparse `SavedViewPatch`.

### Fixed — two capability gaps reported as caller errors

`GET /media/{key}` with no blob store, and the GraphQL endpoint in a
deployment that does not run GraphQL, both answered `422 VALIDATION` — the
same status and code as a malformed request. Retry logic could not tell a
permanent capability gap from a caller error, so a client kept retrying an
upload against a deployment that structurally cannot accept it. Both now
answer `501 FEATURE_DISABLED`.

The Go client gains the three error codes it was missing —
`FEATURE_DISABLED`, `CURSOR_CONFLICT` and `CURSOR_EXPIRED`, the last two
being the two recovery branches every feed consumer must implement — with
matching `errors.Is` sentinels. `docs/api-stability.md` now lists all twelve.
`TestErrorCodeContract` holds the OpenAPI enum, the client constants and the
stability document equal.

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

### Security — media values may only reference an object key the tenant owns

`POST /api/v1/values` accepted raw media metadata, so a caller could write a
media value naming any `object_key`. Download authorization asks "does any value
row in my tenant reference this key", so minting that row was enough to read
another tenant's file — and removing it was enough to delete their bytes. Object
keys are not secret: they leak into revision payloads, CSV exports and URLs.

- A media write that did not come from `UploadMedia` must name an object key the
  tenant already owns, and **inherits that key's stored metadata**. The declared
  MIME type and size were attacker-chosen, so the media constraint's allowlist
  and the upload path's content sniffing were both bypassable; they are now
  taken from the stored value and the caller's copy is discarded. An unknown key
  and another tenant's key produce the same error, so ownership is not probeable
  (#276).
- **Blob GC is reference-counted.** Two values sharing an object key meant
  removing either deleted the bytes out from under the other. A key another row
  still references — in any tenant, archived rows included — keeps its bytes
  (#276).
- `Remove` registers its blob GC on the transaction like every other archival
  path, instead of deleting unconditionally after the call returned. It
  previously deleted the bytes even when the removal rolled back.

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

### BREAKING — REST behaviour

**Four** inconsistencies where the API contradicted itself, released as a minor
under the carve-out in [api-stability.md](docs/api-stability.md#release-versioning):
in each case the documented contract was already the new behaviour and the old
one was the defect. Each is still visible to clients, so review before
upgrading; the first can reject requests that previously succeeded.

(This section said "three" and sat under a `Changed` heading. It is four, and
a change that rejects previously-accepted requests belongs under BREAKING.)

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
- **A bad `?limit=` or `?cursor=` on `GET /activity` answers `422`, not `500`.**
  A caller's malformed pagination argument was reported as an internal server
  error, so it appeared on an error-rate dashboard as a service fault and gave
  the caller nothing to correct (#258).

### Fixed

This section was missing entirely from the 1.2.0 release. Four fixes shipped in
it and were documented nowhere:

- **Enabling tracing was a hard boot failure.** Setting
  `OTEL_EXPORTER_OTLP_ENDPOINT` — the only way to turn tracing on — made `Init`
  return an error, and `cmd/flexitype` turned that into a refusal to start. So
  the observability feature could not be enabled at all, and the failure looked
  like a bad endpoint rather than a bug (#257).
- **The in-memory backend silently dropped rows from paginated listings.** A
  keyset page could omit records with no error and no gap in the cursor, so a
  full walk returned an incomplete set that looked complete (#263).
- **`formula.Parse` silently truncated at an unrecognised character.** The
  parser stopped early and returned what it had, so `application/computed`
  materialized a wrong value for every entity of the type, permanently and
  without an error. **Behaviour change against stored data:** a formula that
  appeared to work because it was being truncated now fails to parse, and the
  attribute reports the error instead of a wrong number (#266).
- **A change-set could write across tenants.** A change-set carrying another
  tenant's attribute or entity ids archived that tenant's value on publish. See
  the 1.1.0 security note below — the fix landed there and was omitted from its
  security list (#222).

## [1.1.0] — 2026-07-18

A post-1.0 independent review (security, architecture, performance and
coding-standards) plus follow-ups — every issue implemented and merged. The
REST API (`/api/v1`), the storage schema (forward-only migrations), and the
supported Go facade stay backward-compatible; changes to them are additive.

### BREAKING — `pkg/db.Transactor` (documented after the fact)

`docs/api-stability.md` listed `pkg/db: Transactor` as a supported extension
port through 1.0. That listing was wrong, and this release changed the type
incompatibly without saying so. Anyone who read the stability document and
supplied their own `Transactor` fails to compile on the minor bump.

`db.Transactor` lost its query surface. Repositories now take an opaque
`db.Tx` marker, and the interface is sealed: only this package's transaction
types, and out-of-package types embedding `db.TxMarker`, satisfy it.

To repair an out-of-module implementation, embed the marker:

```go
type myTx struct {
    db.TxMarker // add this
    // …
}
```

`pkg/db` is no longer listed as an extension port. No facade option ever
accepted a `Transactor` — `New` takes an `*sqlx.DB` and builds one itself —
so the type is internal wiring and continues to change without notice.

### Security

- **A change-set could write across tenants** — a change-set carrying another
  tenant's attribute or entity ids archived that tenant's value on publish. The
  publish path now re-resolves every staged id in the publishing tenant's
  scope. This fix shipped in 1.1.0 and was omitted from this list (#222).
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

[Unreleased]: https://github.com/zkrebbekx/flexitype/compare/v1.4.0...HEAD
[1.4.0]: https://github.com/zkrebbekx/flexitype/compare/v1.3.0...v1.4.0
[#465]: https://github.com/zkrebbekx/flexitype/pull/465
[#466]: https://github.com/zkrebbekx/flexitype/pull/466
[#467]: https://github.com/zkrebbekx/flexitype/pull/467
[#474]: https://github.com/zkrebbekx/flexitype/issues/474
[#497]: https://github.com/zkrebbekx/flexitype/issues/497
[#475]: https://github.com/zkrebbekx/flexitype/issues/475
[#507]: https://github.com/zkrebbekx/flexitype/issues/507
[#502]: https://github.com/zkrebbekx/flexitype/issues/502
[#488]: https://github.com/zkrebbekx/flexitype/pull/488
[#509]: https://github.com/zkrebbekx/flexitype/pull/509
[#510]: https://github.com/zkrebbekx/flexitype/pull/510
[#511]: https://github.com/zkrebbekx/flexitype/pull/511
[#513]: https://github.com/zkrebbekx/flexitype/pull/513
[#514]: https://github.com/zkrebbekx/flexitype/pull/514
[#515]: https://github.com/zkrebbekx/flexitype/pull/515
[#516]: https://github.com/zkrebbekx/flexitype/pull/516
[#517]: https://github.com/zkrebbekx/flexitype/pull/517
[#518]: https://github.com/zkrebbekx/flexitype/pull/518
[#520]: https://github.com/zkrebbekx/flexitype/pull/520
[#521]: https://github.com/zkrebbekx/flexitype/pull/521
[#522]: https://github.com/zkrebbekx/flexitype/pull/522
[#523]: https://github.com/zkrebbekx/flexitype/pull/523
[#524]: https://github.com/zkrebbekx/flexitype/pull/524
[#525]: https://github.com/zkrebbekx/flexitype/pull/525
[#526]: https://github.com/zkrebbekx/flexitype/pull/526
[#527]: https://github.com/zkrebbekx/flexitype/pull/527
[#528]: https://github.com/zkrebbekx/flexitype/pull/528
[#529]: https://github.com/zkrebbekx/flexitype/pull/529
[#530]: https://github.com/zkrebbekx/flexitype/pull/530
[#531]: https://github.com/zkrebbekx/flexitype/pull/531
[#532]: https://github.com/zkrebbekx/flexitype/pull/532
[#533]: https://github.com/zkrebbekx/flexitype/pull/533
[#534]: https://github.com/zkrebbekx/flexitype/pull/534
[#536]: https://github.com/zkrebbekx/flexitype/pull/536
[#539]: https://github.com/zkrebbekx/flexitype/pull/539
[#540]: https://github.com/zkrebbekx/flexitype/pull/540
[#541]: https://github.com/zkrebbekx/flexitype/pull/541
[#547]: https://github.com/zkrebbekx/flexitype/issues/547
[#548]: https://github.com/zkrebbekx/flexitype/issues/548
[#549]: https://github.com/zkrebbekx/flexitype/issues/549
[#550]: https://github.com/zkrebbekx/flexitype/issues/550
[#551]: https://github.com/zkrebbekx/flexitype/issues/551
[#552]: https://github.com/zkrebbekx/flexitype/issues/552
[#553]: https://github.com/zkrebbekx/flexitype/issues/553
[1.5.0]: https://github.com/zkrebbekx/flexitype/compare/v1.4.0...v1.5.0
[1.3.0]: https://github.com/zkrebbekx/flexitype/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/zkrebbekx/flexitype/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/zkrebbekx/flexitype/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/zkrebbekx/flexitype/releases/tag/v1.0.0

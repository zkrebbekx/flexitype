# Changelog

All notable changes to flexitype are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0
(see [API stability](docs/api-stability.md)).

## [Unreleased]

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

[Unreleased]: https://github.com/zkrebbekx/flexitype/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/zkrebbekx/flexitype/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/zkrebbekx/flexitype/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/zkrebbekx/flexitype/releases/tag/v1.0.0

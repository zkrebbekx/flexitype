# Upgrades, rollback and migrations

This page states what flexitype guarantees across a version change, and the
rules a migration must follow so an upgrade never blocks a live fleet.

For backing up and restoring data, see [backup-restore.md](backup-restore.md).
For the API compatibility policy, see [api-stability.md](api-stability.md).

## The compatibility contract

- **A release's migrations stay compatible with the previous release's
  binary.** Release N may add tables, columns and indexes, and may add
  nullable or defaulted columns to existing tables. It must not drop or rename
  anything release N-1 reads, and must not add a constraint that N-1's writes
  would violate.
- **Rollback means redeploying the previous binary.** It does not mean running
  `MigrateDown`. Because of the rule above, release N-1 runs correctly against
  release N's schema.
- **`MigrateDown` is for local development and reversibility tests only.** It
  is never called at startup. Running it against a deployment that holds data
  will drop what the reverted migrations created.
- **Removing something takes two releases.** Release N stops reading the
  column; release N+1 drops it. That way a rollback from N+1 to N still works.

## Rolling deploys

Every replica calls `Migrate` at startup, and they serialize, so it is safe
for the whole fleet to start at once. The first new pod migrates; the rest
wait and then find nothing to do.

That holds **through a transaction-mode pooler**, which is what the fleet
usually connects through. The runner takes no session-scoped lock: mutual
exclusion is a lease row (`flexitype_schema_lock`), and each migration
additionally claims its version row inside the transaction that applies it, so
the database serializes the runners for exactly as long as an apply takes. A
runner that dies mid-run frees the lease by expiry rather than stranding a
lock on a pooled backend that only a manual `pg_terminate_backend` could
clear.

During the rollout the previous generation serves against the new schema. That
is the state the contract above is written for. The binary reports it:

```
WARN  the database has schema migrations this binary does not know; another
      release migrated it (expected briefly during a rolling deploy, otherwise
      this binary is older than the fleet)  versions=[21]
```

If that warning persists after a rollout finishes, a replica of an older
release is still running.

Embedders can read the same fact with `Service.SchemaDrift(ctx)`.

## How migrations are applied

Each migration runs in **its own transaction**, in version order, while the
runner holds the migration lease.

The lease is a row, not a session lock, and it is refreshed between
statements; a runner that loses it — because one statement outlasted the
15-minute TTL and another runner took over — stops rather than applying DDL
alongside the new holder. Correctness does not rest on the lease alone: a
transactional migration inserts its version row **before** the DDL, in the
same transaction, so a second runner's insert blocks on the primary key and
then finds the version taken.

Applying all pending migrations in one transaction would be tidier, but it
cannot coexist with what a large deployment needs. `CREATE INDEX CONCURRENTLY`
is rejected inside a transaction block. And a data backfill inside the schema
transaction holds that transaction's DDL locks for the whole scan — which is
how a whole-table backfill came to block every value write in the fleet for the
duration of an upgrade.

The consequence to plan for: a failure part-way leaves earlier migrations
applied. The run stops at the failing migration and the next boot resumes from
there.

### File directives

A migration file may declare how it must run:

```sql
-- +flexitype:no-transaction
```

The runner then sends the file's statements **one at a time, outside any
transaction**. Use it for statements Postgres forbids inside a transaction
block:

- `CREATE INDEX CONCURRENTLY` / `DROP INDEX CONCURRENTLY`
- `ALTER TYPE ... ADD VALUE`

An unknown directive is a hard error, so a typo cannot silently fall back to
the blocking default.

### Rules for writing a migration

1. **Index builds on a large table use `CONCURRENTLY`, in a no-transaction
   file.** `flexitype_attribute_value` is always a large table. A plain
   `CREATE INDEX` on it takes a lock that conflicts with every write and holds
   it for the whole build.
2. **Every statement in a no-transaction file is idempotent.** The file is
   replayed in full if it is interrupted. Use `IF NOT EXISTS` / `IF EXISTS`.
3. **A concurrent index build drops an invalid namesake first.** A failed
   `CREATE INDEX CONCURRENTLY` leaves an `INVALID` index behind, and
   `IF NOT EXISTS` would then skip it forever. The pattern:

   ```sql
   DO $$
   BEGIN
       IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid
                   WHERE c.relname = 'idx_name' AND NOT i.indisvalid) THEN
           EXECUTE 'DROP INDEX idx_name';
       END IF;
   END $$;

   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_name ON ...;
   ```
4. **A no-transaction file creates no tables.** A table is the one object whose
   duplicate creation cannot be made safe under replay. Schema objects belong
   in a transactional file.
5. **Data movement is not a migration.** See below.
6. **A new constraint is added `NOT VALID` first, then validated separately.**
   Adding a validated constraint scans and locks the table.

`TestEmbeddedMigrationDirectives` enforces rules 1 and 4 in CI: a file that
uses `CONCURRENTLY` must declare the directive, a file that declares it must
not, and no such file may create a table.

## Backfills

Moving data belongs in a **backfill step** (`migrate_backfill.go`), not in a
`.sql` file. Steps run after the schema is in place, in bounded batches, each
committing on its own, tracked in `flexitype_schema_backfill`.

A backfill step must be:

- **Idempotent and resumable.** It is retried from wherever it stopped, on
  every boot, until it reports completion.
- **Safe against concurrent writers.** Correctness must never depend on the
  fleet being idle. The schema change that precedes the backfill is
  responsible for keeping *new* writes right; the backfill only catches up
  history.
- **Bounded per batch.** No statement may scan the whole table.

The registered steps are:

| Step | Requires | What it catches up |
| --- | --- | --- |
| `000019_entity_summary` | migration 19 | one summary row per entity that existed before the projection's trigger was installed |

While a backfill is still running, the derived data it maintains is incomplete
but never wrong. For `000019_entity_summary` that means an entity written
before the upgrade, and not written since, is missing from the entity browser
until the step reaches it.

## Disabling migration on start

Set `FLEXITYPE_MIGRATE_ON_START=false` to run migrations out of band — a
dedicated job before the rollout, rather than the first pod to start. The
service then boots against whatever schema it finds, and the drift warning
above tells you if that schema is older than the binary expects.

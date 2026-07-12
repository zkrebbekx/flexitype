# Backup, restore & schema portability

flexitype's data lives in Postgres, so the authoritative backup story is
Postgres' own. On top of that, the **schema bundle** endpoints let you move
a schema (types, attributes, relationship definitions, dependencies)
between instances without moving data — for seeding, promotion, or version
control.

> For the opposite operation — permanently destroying a data subject's data
> (right to erasure / GDPR / CCPA) — see [Data erasure](erasure.md).

## Full backup and restore (Postgres)

flexitype owns no state outside its database. A logical dump captures
everything — schema and data:

```bash
# Back up
pg_dump --format=custom --no-owner --file flexitype.dump "$DATABASE_URL"

# Restore into a fresh, empty database
createdb flexitype_restored
pg_restore --no-owner --dbname "$RESTORE_URL" flexitype.dump
```

Notes:

- Restore into an **empty** database. flexitype runs its own migrations on
  boot; a dump already contains the migrated schema, so restore first, then
  start the service against the restored database (it will see migrations as
  already applied).
- `--no-owner` avoids role-ownership mismatches between environments.
- For point-in-time recovery, use Postgres WAL archiving / a managed
  provider's PITR — nothing flexitype-specific is required.

## Schema bundles

A bundle is a JSON document describing a tenant's **schema only** — no
entity values. Everything is keyed by internal name (never by ID), so a
bundle produced on one instance imports cleanly into another.

### Export

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  https://flexitype.example.com/api/v1/schema/export > schema.json
```

Commit `schema.json` to version control to track schema changes over time,
or use it as a seed for new environments.

### Import

```bash
curl -sS -X POST -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @schema.json \
  https://flexitype.example.com/api/v1/schema/import
```

Import is **idempotent**: it creates only objects that are missing (matched
by internal name) and skips those already present, applying them in
dependency order (types, then attributes, then relationship definitions,
then dependencies). The response reports created/skipped counts per kind:

```json
{
  "types": { "created": 2, "skipped": 0 },
  "attributes": { "created": 5, "skipped": 1 },
  "relationship_definitions": { "created": 1, "skipped": 0 },
  "dependencies": { "created": 3, "skipped": 0 }
}
```

Because it is idempotent, a partial import (interrupted midway) is completed
simply by re-running the same bundle. Import is **not** a single
transaction — treat it as a converging operation, not an atomic one.

### Promoting a playground schema to a real instance

The [browser playground](https://zkrebbekx.github.io/flexitype/) runs the
service in-memory, so it is a safe place to design a schema. When you are
happy with it:

1. In the playground, export the schema (the console's export action, or the
   `/api/v1/schema/export` endpoint the playground serves in-browser).
2. Save the resulting `schema.json`.
3. Import it into your real instance with the `curl` command above.

The same flow works instance-to-instance (staging → production): export
from one, import into the other.

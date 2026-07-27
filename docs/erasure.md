# Data erasure (right to erasure)

Everyday deletes in flexitype are **soft**: archiving a value or removing an
entity sets `archived_at` and filters the row out of live reads, but the data
stays on disk (so it can be restored, audited, and read as history). That is the
right default — but it is not enough when a data subject exercises a **right to
erasure** (GDPR Art. 17 / CCPA) and their data must be permanently destroyed.

Erasure closes that gap. It is an explicit, **irreversible hard delete** that
physically removes rows (archived rows included) and garbage-collects the
backing media blobs. It is **admin-scoped** and **audited**: the erasure itself
is written to the activity log, so you retain proof that the data is gone even
though the data is not.

> ⚠️ Erasure cannot be undone. There is no archive, no revision, no restore. Run
> a [backup](backup-restore.md) first if you need a recovery point.

## Per-entity erasure

Permanently removes **one** entity: every attribute value (live and archived),
every revision, every relationship link it participates in (as parent or child),
and the backing blobs of any media values. The value and relationship deletes
plus the audit entry are one unit of work.

```bash
curl -sS -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  https://flexitype.example.com/api/v1/entities/{typeDefinitionID}/{entityID}/purge
```

Returns a receipt of what was erased:

```json
{
  "entity_id": "e-42",
  "values_purged": 7,
  "revisions_purged": 3,
  "relationships_gone": 2,
  "search_docs_purged": 0,
  "media_blobs_purged": 1,
  "records_redacted": 12,
  "media_blobs_failed": 0,
  "unpurged_blob_keys": []
}
```

An entity with no values, revisions or links is reported `404 Not Found`.

This is distinct from the soft-delete `DELETE /entities/{typeDefinitionID}/{entityID}`,
which only **archives**.

## Per-tenant erasure

Permanently removes the calling tenant's entity **data** — every attribute
value, revision, relationship link and search-projection document — and
garbage-collects the media blobs. The tenant is taken from the credential.

```bash
curl -sS -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  https://flexitype.example.com/api/v1/admin/purge
```

```json
{
  "values_purged": 1240,
  "revisions_purged": 310,
  "relationships_gone": 205,
  "search_docs_purged": 640,
  "media_blobs_purged": 88,
  "records_redacted": 4021,
  "media_blobs_failed": 2,
  "unpurged_blob_keys": ["t1/9f/a0c2…", "t1/1b/77de…"]
}
```

Per-tenant erasure is deliberately scoped to **entity data**. It does **not**
remove the tenant's type/attribute **definitions**, unit families, saved views,
change-sets, dedup rules, webhook subscriptions, or the activity log — the
schema and control plane survive so the tenant can keep operating or be
decommissioned separately (via the provisioning API).

## From Go (embedded)

The same primitives are on the value interactor:

```go
it := svc.Interactors(ctx) // ctx carries the tenant
report, err := it.Values().PurgeEntity(ctx, typeDefinitionID, entityID)
// ...
report, err = it.Values().PurgeTenant(ctx) // tenant from ctx
```

## Notes

- **Copies of the value** — deleting the value rows is not enough. The event
  log embeds the value in every set/updated/removed payload, and the feed
  serves those payloads until retention pruning: 7 days by default,
  arbitrarily long by configuration, and **forever** for rows never expanded,
  because pruning requires a `feed_seq`. The activity log persists a value
  snapshot in every entry's before/after state. Webhook deliveries read the
  event payload through their envelope reference, so redacting the event log
  covers them too.

  Both are redacted in the erasure transaction, and the count lands in
  `records_redacted`. **Redacted, not deleted**: the activity log has to
  survive for the erasure to be provable, and the event feed's sequence is
  gapless by design — deleting rows would break the one guarantee a consumer
  relies on. The row and its identifiers stay; the value content becomes a
  marker. A tenant purge leaves schema history alone, because it erases entity
  data rather than definitions.

- **Access control** — both endpoints require the `admin` scope
  (`requireAdmin`); a non-admin credential gets `403 Forbidden`.
- **Audit** — each erasure records an activity entry with the `purged` action
  and the counts, in the same transaction as the value/link deletes. The audit
  log is intentionally **not** purged, so the erasure remains provable.
- **Media blobs** — object keys are gathered before the DB rows are deleted and
  the blobs are removed after commit (best effort, mirroring the archival GC
  path); a storage hiccup never fails the erasure and a later sweep can
  reconcile.

  **Check `media_blobs_failed` before you record the request as satisfied.**
  A non-zero value means the erasure did not fully succeed: those blobs are
  still in object storage, and `unpurged_blob_keys` names every one of them
  so an operator can remove them by hand. The receipt reports the residue
  honestly rather than a false success, which is the whole reason those two
  fields exist — and they were missing from both samples above and from the
  OpenAPI schema.
- **Search & revisions** — the search projection is event-driven and revisions
  are stored outside the value write transaction, so their purge runs on their
  own stores within the same call; the deletes are idempotent under retry.

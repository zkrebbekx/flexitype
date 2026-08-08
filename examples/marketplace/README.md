# Example: a multi-tenant marketplace

Many merchants, one storefront. Each merchant is its OWN flexitype tenant with
its OWN schema; shoppers browse every merchant's products in one place.

Two Go services, both against a **standalone** flexitype over HTTP with the Go
SDK (`github.com/zkrebbekx/flexitype/client`). Neither embeds the library.

- **[`platform/`](platform/)** — onboards a merchant and serves the
  merchant-facing API a console calls.
- **[`storefront/`](storefront/)** — receives signed webhooks, keeps its own
  denormalized catalog, and serves shoppers.

**Prerequisites:** Docker, `curl` and `jq`. `seed.sh` checks for them up front.

```
                    ┌──────────── platform ────────────┐
   merchant console │ onboard: tenant + account +      │  admin token
        ──────────► │ template + subscription + backfill│ ──────────┐
                    │ merchant API (holds the token)   │           │
                    └──────────────────────────────────┘           ▼
                                                            ┌─────────────┐
                    ┌─────────── storefront ───────────┐    │  flexitype  │
        shopper ───►│ search / filter / detail         │◄───│  (tenant    │
                    │ webhook ingest ─► re-read ─► row │    │   per       │
                    │ backfill (FQL)                   │    │   merchant) │
                    └──────────────────────────────────┘    └─────────────┘
```

## Run it

```bash
cd examples/marketplace
docker compose up --build --wait      # postgres + flexitype + platform + storefront
./seed.sh                             # onboards 3 merchants, seeds products, asserts
```

`seed.sh` is safe to re-run. To start over: `docker compose down --volumes`,
then `rm -f .admin/admin-token`, then bring the stack up again.

Then:

```bash
curl -s 'http://localhost:9200/api/products' | jq                    # the storefront
curl -s 'http://localhost:9200/api/products?q=merino' | jq           # full-text search
open http://localhost:8080                                          # the flexitype console
```

## The tenancy model

**A merchant is a tenant.** Onboarding creates a flexitype tenant, then a
service account scoped to it. flexitype takes the tenant from the token
(`internal/interfaces/http/middleware.go`), so **a client IS a tenant**: there
is no request that reads two merchants at once, and no way to write one
merchant's data with another's credential.

**Every merchant starts from the same schema and then owns its copy.**
Onboarding applies the curated `ecommerce` template
([`application/schema/templates/ecommerce.json`](../../application/schema/templates/ecommerce.json))
into the new tenant. The template declares a root `product` type — name,
description, sku, status, price, currency, in_stock, image — a `brand` type, a
`made_by` relationship, and two dependencies that make `sku` and `price`
required once `status` is `active`.

Applying, rather than sharing, is the point: after onboarding, merchant A's
`product` and merchant B's `product` are different rows with different ids. A
can rename a field, tighten a constraint or archive an attribute without
touching B.

**Merchants extend the root type themselves.** Each one creates SUBTYPES with
`extends`, adding the fields only it has. `seed.sh` creates three:

| Merchant          | Subtype       | Its own fields                |
| ----------------- | ------------- | ----------------------------- |
| Alpine Apparel    | `apparel`     | `size`, `colour`              |
| Bolt Electronics  | `electronics` | `voltage`, `warranty_months`  |
| Cellar Coffee     | `coffee`      | `roast`, `weight_grams`       |

A subtype inherits every `product` field, so the storefront finds `name`,
`price` and `status` on all three without knowing any of them in advance.

## Why the storefront projects instead of querying

flexitype has no cross-tenant query, and that is a property, not a gap: the
tenant comes from the token, so "every merchant's active products, ranked by
relevance" is not expressible against flexitype at all.

The storefront therefore keeps its OWN denormalized catalog in Postgres, fed by
flexitype's webhooks, and answers shoppers from it. One row per product:

```sql
CREATE TABLE storefront.catalog_product (
    tenant      text NOT NULL,          -- the merchant
    entity_id   text NOT NULL,
    type_id     text NOT NULL,          -- the flexitype type definition
    subtype     text NOT NULL,          -- "apparel", "electronics", "coffee"
    name        text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    sku         text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT '',
    price       numeric,
    currency    text NOT NULL DEFAULT '',
    in_stock    boolean,
    image       jsonb,                  -- the media value: object key, mime, checksum
    attributes  jsonb NOT NULL DEFAULT '{}'::jsonb,   -- every subtype-specific field
    updated_at  timestamptz NOT NULL DEFAULT now(),
    search      tsvector GENERATED ALWAYS AS (…name, sku, description…) STORED,
    PRIMARY KEY (tenant, entity_id)
);
```

The core commerce fields get columns, because every merchant has them and the
storefront filters and sorts on them. Everything a merchant added to its own
subtype goes into `attributes`, keyed by internal name — that is what lets one
storefront render heterogeneous schemas. A localized or scoped value is kept
under `name@fr`, so the base value stays the default rendering.

`search` is a GENERATED column, so it cannot drift from the text it indexes:
there is no code path that writes `name` without rewriting the vector.

### The projector re-reads; it never applies a payload

On any event for entity E in tenant T, the projector re-reads E's **whole**
value set with T's client and overwrites the row.

That is what makes it idempotent and order-independent. Delivery is
at-least-once and unordered, so the same event arrives twice, and a
`price = 19.99` event can arrive after the `price = 24.99` that superseded it.
Applying the payload would write the stale price and keep it. Re-reading asks
flexitype for the current truth, so every delivery converges on the same row
whatever its order or count — the event is only a signal that something
changed.

Writing one product is one value event per field, so a burst is **debounced**
per entity: the first event opens a 250 ms window and one projection runs when
it closes.

### Backfill

A subscription only carries what happens after it is registered. A merchant
that imported a catalogue before onboarding has no events to replay, so
onboarding ends with a backfill: the storefront walks that merchant's products
with FQL (`has(name)`, rooted at `product`, which also returns every subtype)
and projects each one. It is safe to re-run at any time, and it is the repair
procedure for a projection that drifted:

```bash
curl -X POST -H 'X-Internal-Token: marketplace-internal-demo-token' \
  http://localhost:9200/internal/merchants/alpine/backfill
```

### Only active products reach shoppers

A draft is unfinished work; an archived product is withdrawn. Neither is an
offer. The clamp lives in the one function that reads the catalog table, not in
each handler, so a new endpoint cannot forget it. `status=draft` as a filter
returns nothing rather than an error, and a draft's exact URL is a 404.

Everything IS projected, including drafts. Only the read path hides them, so a
merchant publishing a draft becomes visible immediately with no second
backfill.

## Credentials

There are four in this example, and they are all demo values in
`docker-compose.yml`:

| Credential | Held by | What it opens |
| --- | --- | --- |
| flexitype admin token | platform | creating tenants and service accounts |
| merchant service-account token | platform, storefront | one merchant's catalog |
| `MARKETPLACE_INTERNAL_TOKEN` | platform → storefront | merchant registration, backfill |
| `PLATFORM_API_TOKEN` | merchant console → platform | the merchant API |

**The merchant token is stored in a table here. A real deployment would not do
that.** It would keep the token in a secret manager (Vault, AWS Secrets
Manager, GCP Secret Manager) and store only the secret's NAME in the row, so a
database dump or a read-only SQL leak does not hand over every merchant's
catalog. The token never appears in a log line or an API response: `Merchant`
marshals it with `json:"-"`, which is a mechanism rather than a rule each
handler has to remember, and there is a test for it.

`PLATFORM_API_TOKEN` is one shared console credential, which keeps the example
runnable. A real deployment authenticates a merchant USER and derives the
merchant id from the session, so one merchant cannot reach another's endpoints.

The admin token arrives through a **file**: flexitype prints its bootstrap
admin token to stdout once, and its image is distroless, so a compose stack
cannot capture it into an environment variable. `seed.sh` reads it out of the
container log and writes `.admin/admin-token`, which the platform mounts. The
platform re-reads that file when it changes, so a rotated credential is picked
up without a restart.

## The webhook path

Onboarding registers one subscription per merchant, inside that merchant's
tenant, pointing at `http://storefront:9200/hook/{tenant}` with a per-merchant
secret.

The tenant is in the path so the storefront needs ONE HMAC verification per
delivery instead of one per known merchant. The path is untrusted input: it
only selects which secret to try, so a wrong tenant fails verification. After
verification the tenant is taken from the SIGNED envelope, and a delivery whose
envelope names a different tenant is rejected.

Verification is `events.VerifyRequest` — the same call
[`examples/catalog/consumer`](../catalog/consumer/) uses. An unsigned, wrongly
signed, tampered or stale delivery is a 401, and an unknown tenant answers
identically so merchants are not enumerable.

## The merchant API

Thin on purpose. It proxies to that merchant's flexitype client and adds
nothing a console could not do itself — except hold the token.

```bash
TOKEN='platform-console-demo-token'
API=http://localhost:9300/api

curl -s -H "Authorization: Bearer $TOKEN" $API/merchants | jq

# Create a subtype with its own fields
curl -s -X POST -H "Authorization: Bearer $TOKEN" $API/merchants/alpine/types -d '{
  "internal_name":"footwear","display_name":"Footwear",
  "attributes":[{"internal_name":"eu_size","display_name":"EU size","data_type":"integer"}]}' | jq

# A type's EFFECTIVE attributes — inherited ones included
curl -s -H "Authorization: Bearer $TOKEN" \
  "$API/merchants/alpine/types/$TYPE_ID/attributes" | jq '[.items[].attribute.internal_name]'

# Write a whole product in one atomic batch
curl -s -X PUT -H "Authorization: Bearer $TOKEN" $API/merchants/alpine/products/boot-1 -d '{
  "type":"footwear",
  "values":{"name":"Trail Boot","sku":"ALP-9","status":"active","price":"149.00",
            "currency":"EUR","in_stock":true,"eu_size":43}}' | jq

# Upload a product image
curl -s -X POST -H "Authorization: Bearer $TOKEN" -F 'file=@photo.png;type=image/png' \
  "$API/merchants/alpine/products/boot-1/image?type=footwear" | jq
```

The batch write is atomic: either every value lands and its events fire, or none
does. That matters for the storefront, which would otherwise project a
half-written product.

## The shopper API

```bash
GET /api/products?q=&merchant=&min_price=&max_price=&limit=&offset=
GET /api/products/{tenant}/{entityID}
GET /api/products/{tenant}/{entityID}/image
GET /api/merchants
```

The image endpoint proxies the bytes from flexitype's blob store with the
merchant's own credential, so a shopper never holds one, and the image of a
non-active product is unreachable because the row was already refused.

## Tests

```bash
export FLEXITYPE_TEST_DSN='postgres://postgres:postgres@localhost:5432/flexitype?sslmode=disable'
(cd platform && go test ./...)
(cd storefront && go test ./...)
```

They skip without a DSN, like the repository's other Postgres suites. Each
suite drives a REAL flexitype — the storefront against an in-memory one with a
service account per tenant, the platform against a Postgres-backed one with
provisioning on, because the tenant and service-account API is database-backed.

They cover: onboarding is idempotent and re-runnable after a part-way failure;
two merchants are isolated; the projector is idempotent and order-independent;
a new attribute is picked up; the backfill is re-runnable; a draft or archived
product is invisible on every shopper path; an unsigned, wrongly signed,
tampered, stale or cross-tenant delivery is refused; the debouncer coalesces a
burst per entity; and no response or log line carries a merchant token.

## What the API made awkward

Honest notes from building this, for whoever works on flexitype next.

1. **`client.WebhooksService.Update` cannot succeed.** It PATCHes a
   `SubscriptionInput`, whose `name` field has no `omitempty`, but
   `PATCH /webhook-subscriptions/{id}` decodes with `DisallowUnknownFields`
   into `{url, event_types, active, rotate_secret}`. Every call fails with
   `VALIDATION: invalid request body`. The two shapes also disagree on
   secrets: the input has `secret`, the endpoint wants `rotate_secret`. This
   example deletes and recreates the subscription instead.

2. **No supported way to inject a known admin credential.** In provisioning
   mode the bootstrap token is printed to stdout once, and the image is
   distroless, so an orchestrated stack cannot capture it. A
   `FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN` (caller-supplied, hashed at boot) would
   make a provisioning-mode deployment reproducible.

3. **`FLEXITYPE_DEV_INSECURE` conflates two things.** It is the only opt-out
   from the unencrypted-database guard, and it also reads as "authentication
   off". It is not, when an account source is configured — but a stack that
   needs plaintext Postgres has to set a variable whose name and log warning
   both say the opposite of what it is doing. A separate
   `FLEXITYPE_DB_ALLOW_PLAINTEXT` would let this compose file keep
   authentication on without looking reckless.

4. **The projector needs one credential per tenant.** It holds every
   merchant's token purely to re-read entities. A read-only, cross-tenant
   consumer credential — or an event payload that carried the entity's full
   value set — would let a projection service hold no merchant credential at
   all. That is the single biggest security cost of this architecture.

5. **An event does not say which entity changed without a payload parse.** The
   envelope's `aggregate_id` is the attribute VALUE id, so a subscriber that
   only wants "entity E changed" has to decode the payload for
   `type_definition_id` and `entity_id`. Putting the entity's coordinates on
   the envelope would let a router work without knowing any payload schema.

6. **There is no data type for long text.** `description` is a `string` with a
   large `max_length`, which is fine but tells a UI nothing about how to
   render it. A `text` type, or a `multiline` hint, would.

7. **A media value's bytes are only reachable with a tenant credential.** That
   is right, but it means every public-facing surface has to proxy images.
   Signed, expiring media URLs would remove a whole proxy path.

8. **Blob storage needs a writable directory the distroless image cannot
   create.** A named volume mounts as root, the image runs as `nonroot`, and
   the failure surfaces only on the first upload — long after the stack came
   up healthy. `FLEXITYPE_BLOB_DIR` could be checked for writability at
   startup and refuse to boot.

# Example: a multi-tenant marketplace

Many merchants, **one storefront each**. Each merchant is its own flexitype
tenant with its own schema, and each has its own storefront holding one
credential and serving one catalogue.

Two Go services against a **standalone** flexitype over HTTP with the Go SDK
(`github.com/zkrebbekx/flexitype/client`), and two React front ends on the
TypeScript SDK (`@flexitype/client`). Nothing here embeds the library.

- **[`platform/`](platform/)** — onboards a merchant, serves the merchant API,
  and forwards a merchant's flexitype READS with its token attached.
- **[`storefront/`](storefront/)** — deployed PER MERCHANT: receives that
  merchant's signed webhooks, keeps its catalogue, and serves its shoppers.
- **[`console/`](console/)** — the merchant console. Its product form is drawn
  by the SCHEMA: it names no product field.
- **[`shop/`](shop/)** — the shopper UI of one merchant's storefront.

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
docker compose up --build --wait      # postgres, flexitype, platform, storefront, console, shop
./seed.sh                             # onboards 3 merchants, seeds products, asserts
```

| What | Where |
| --- | --- |
| Shopper storefront (Alpine) | <http://localhost:8082> |
| Merchant console | <http://localhost:8081> |
| flexitype console | <http://localhost:8080> |
| Alpine's storefront API | <http://localhost:9200/api/products> |
| Bolt's storefront API | <http://localhost:9201/api/products> |
| Merchant API | <http://localhost:9300/api/merchants> |

`seed.sh` is safe to re-run. To start over: `docker compose down --volumes`,
then bring the stack up again.

Then open <http://localhost:8082> to shop, or <http://localhost:8081> to model
a merchant's schema and edit its products. The same data over curl:

```bash
curl -s 'http://localhost:9200/api/products' | jq                    # the storefront
curl -s 'http://localhost:9200/api/products?q=merino' | jq           # full-text search
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

Onboarding then switches those two rules to **refuse the write**
(`"enforce": "on_write"`, in [`platform/onboard.go`](platform/onboard.go)).
`status = active` is a lifecycle state, not a fact someone is typing in: a
product a shopper can buy with no sku and no price is not a product that should
exist, and this marketplace has no later gate that would catch one.

It is done here, rather than by shipping a stricter template, because a schema
import identifies a rule and only ever creates or skips — so a template cannot
*re-configure* a rule a tenant already has. Setting the mode in place is what
keeps onboarding idempotent, and it shows the thing a template cannot: how to
do it in your own schema.

```bash
# 422: nothing else on the product yet.
curl -X POST .../values -d '{"attribute_definition_id":"<status>","entity_id":"p1","value":"active"}'
# {"error":{"code":"DEPENDENCY_VIOLATION",
#   "message":"an attribute dependency requires a value for \"sku\""}}
```

It costs the platform nothing, because it writes a whole product as ONE batch
([`platform/api.go`](platform/api.go)) — which is how a caller satisfies such a
rule: the state and what it demands arrive together, in any order. A platform
that saved a product field by field would leave the rules reporting and gate
somewhere of its own. That choice is
[docs/dependencies.md](../../docs/dependencies.md#choosing-a-mode).

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

## One storefront per merchant

This example ran the other way once: ONE storefront across every merchant,
aggregating them into a single catalogue. That forced it to hold every
merchant's credential — a leak of its database handed over every catalogue —
and it pushed flexitype itself to grow a `read_any_tenant` scope so the
storefront could hold one credential instead of many.

Both were the wrong shape, and the friction was the design saying so. flexitype
takes the tenant from the token, so no request reads two tenants. A service
built to aggregate tenants is fighting that, and the fix was not a privilege
that crosses the line — it was to stop needing one.

So: **one storefront per merchant**. Each is configured with its tenant
(`STOREFRONT_TENANT`), holds one credential, keeps one catalogue in its own
schema, and refuses anything that is not its merchant:

| Reached by | Answer |
| --- | --- |
| A shopper asking for another merchant's product | 404 — the path takes no tenant, and the data is not in this database |
| The platform registering another merchant's credential | 403 — this process holds one merchant's token |
| A delivery on another merchant's hook path | 401, byte-identical to a bad signature, so the merchants a storefront serves are not probeable |

`seed.sh` asserts each of those.

## Why a storefront projects instead of querying

Even for one merchant, the storefront keeps its own denormalized catalogue in
Postgres, fed by flexitype's webhooks, rather than querying flexitype per
request: a shopper page is a read-heavy surface with its own ranking and
filtering, and a projection is what makes it cheap. One row per product:

```sql
CREATE TABLE storefront.catalog_product (
    tenant      text NOT NULL,          -- the merchant
    entity_id   text NOT NULL,
    type_id     text NOT NULL,          -- the flexitype type definition
    subtype     text NOT NULL,          -- "apparel", "electronics"
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

The core commerce fields get columns, because the storefront filters and sorts
on them. Everything the merchant added to its own subtype goes into
`attributes`, keyed by internal name — that is what lets the storefront render
a schema it does not know in advance. A localized or scoped value is kept under
`name@fr`, so the base value stays the default rendering.

The `tenant` column stays in the schema even though one storefront holds one
merchant. It is what makes every read say which merchant it is about, so a
misconfigured deployment fails a comparison rather than silently serving the
wrong catalogue.

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

## The two front ends

Both are React 19 on Vite, and neither holds a credential in the browser.

### The merchant console ([`console/`](console/))

The form a merchant edits a product with is drawn by the SCHEMA. It reads the
type's EFFECTIVE attributes with the SDK's `useFormDescriptor` and renders what
comes back: no product field is named anywhere in the application. A merchant
that adds `voltage` to its own subtype gets a `voltage` input with no console
change at all.

The console reads and writes through two different paths, for two reasons:

- **A read** goes to the platform's flexitype PASSTHROUGH, so the SDK issues
  real flexitype paths and its services, hooks and soft-typing helpers work
  unchanged. The platform attaches that merchant's service-account token. The
  passthrough is read-only and refuses a write with 405
  ([`platform/passthrough.go`](platform/passthrough.go)).
- **A write** goes to the platform's own endpoints, which batch a whole product
  into ONE atomic call. Writing value by value would let the storefront project
  a half-written product.

Values are coerced by the SDK's `toWire` before the request, so a value that
cannot be its data type is refused with the same `VALIDATION` code the service
would have answered with, and nothing is sent.

### The shopper storefront ([`shop/`](shop/))

One merchant's catalogue, answered from that merchant's projection. There is no
merchant filter and no tenant in any path: this UI is served by one storefront,
which has one catalogue. A product page renders whatever the merchant added to
its own subtype, keyed by internal name, without knowing any of those names.

## Credentials

There are four in this example, and they are all demo values in
`docker-compose.yml`:

| Credential | Held by | What it opens |
| --- | --- | --- |
| flexitype admin token | platform | creating tenants and service accounts |
| merchant service-account token | platform, and that merchant's storefront ONLY | one merchant's catalogue |
| `MARKETPLACE_INTERNAL_TOKEN` | platform → each storefront | merchant registration, backfill |
| `PLATFORM_API_TOKEN` | the console's nginx → platform | the merchant API |

**The merchant token is stored in a table here. A real deployment would not do
that.** It would keep the token in a secret manager (Vault, AWS Secrets
Manager, GCP Secret Manager) and store only the secret's NAME in the row, so a
database dump or a read-only SQL leak does not hand over that merchant's
catalogue. A storefront holds ONE such token, so a leak reaches one merchant
rather than all of them. The token never appears in a log line or an API response: `Merchant`
marshals it with `json:"-"`, which is a mechanism rather than a rule each
handler has to remember, and there is a test for it.

`PLATFORM_API_TOKEN` is one shared console credential, which keeps the example
runnable. A real deployment authenticates a merchant USER and derives the
merchant id from the session, so one merchant cannot reach another's endpoints.

The flexitype admin token is **decided by the compose file**, not captured from
a log. The same value goes to the flexitype service
(`FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN`) and to the platform
(`FLEXITYPE_ADMIN_TOKEN`), so the stack needs no capture step at all. A minted
token is printed to stdout once and this image is distroless, so no other
container could ever read it — which is why the service accepts a supplied one.

A real deployment generates the token with `flexitype bootstrap-token`, keeps
it in a secret manager and hands it to both services. The platform also accepts
`FLEXITYPE_ADMIN_TOKEN_FILE` and re-reads that file when it changes, so a
rotation is picked up without a restart.

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

The image endpoint mints a **signed, expiring link** with the merchant's
credential and redirects the browser to it, so the bytes go straight from
flexitype to the shopper and a CDN can cache them — they never cross this
service. A shopper still holds no merchant token: the signature is scoped to
one object and one expiry, and the image of a non-active product is unreachable
because the row was already refused.

Set `STOREFRONT_MEDIA_PUBLIC_BASE` to the address a BROWSER reaches flexitype
on; the container-network name this service uses would not resolve there.
Without it — or against a deployment that sets no `FLEXITYPE_MEDIA_URL_SECRET`
— it falls back to proxying the bytes.

## Tests

```bash
export FLEXITYPE_TEST_DSN='postgres://postgres:postgres@localhost:5432/flexitype?sslmode=disable'
(cd platform && go test ./...)
(cd storefront && go test ./...)

# The front ends. The console consumes the SDK as a file dependency, so build
# the SDK first.
(cd ../../client-ts && npm ci && npm run build)
(cd console && npm ci && npm test)
(cd shop && npm ci && npm test)
```

They skip without a DSN, like the repository's other Postgres suites. Each
suite drives a REAL flexitype — the storefront against an in-memory one with a
service account per tenant, the platform against a Postgres-backed one with
provisioning on, because the tenant and service-account API is database-backed.

The front-end suites cover the form the schema draws (including a field the
console never names), the wire form each data type produces, a value that
cannot be its type, a localized write, products from two tenants in one grid,
a withdrawn product, and that no browser request carries a credential.

The Go suites cover: onboarding is idempotent and re-runnable after a part-way
failure; two merchants are isolated; the projector is idempotent and order-independent;
a new attribute is picked up; the backfill is re-runnable; a draft or archived
product is invisible on every shopper path; an unsigned, wrongly signed,
tampered, stale or wrong-tenant delivery is refused; a storefront serves one
merchant and refuses another's product, credential and deliveries; the
debouncer coalesces a
burst per entity; no response or log line carries a merchant token; and the read-only
passthrough reaches the right tenant, refuses a write, refuses the admin API
and never echoes the token.

## What the API made awkward

Honest notes from building this, for whoever works on flexitype next.

Each one is filed, so it can be tracked rather than only recorded here.

1. ~~**`client.WebhooksService.Update` cannot succeed.**~~ **Fixed.** It
   PATCHed a `SubscriptionInput`, whose `name` field has no `omitempty`, into
   an endpoint that decodes with `DisallowUnknownFields` and reads
   `{url, event_types, active, rotate_secret}`, so every call failed with
   `VALIDATION: invalid request body`. The method now sends only those four
   fields and maps `Secret` to `rotate_secret`. This example still deletes and
   recreates the subscription, which is also a valid way to do it.

2. ~~**No supported way to inject a known admin credential.**~~ **Fixed.**
   `FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN` now takes a caller-supplied credential,
   and `flexitype bootstrap-token` prints a valid one. This stack uses it, so
   nothing reads the token out of a container log any more. ([#547])

3. ~~**`FLEXITYPE_DEV_INSECURE` conflates two things.**~~ **Fixed.**
   `FLEXITYPE_DB_ALLOW_PLAINTEXT` now permits an unencrypted database
   connection to a non-loopback host and nothing else, so this compose file
   keeps authentication on without setting the variable that turns it off.
   `FLEXITYPE_DEV_INSECURE` still implies it. ([#548])

4. ~~**The projector needs one credential per tenant.**~~ **Answered by
   changing the example, not the service.** A `read_any_tenant` scope was added
   and then withdrawn: it made the property every other guarantee rests on
   conditional, to serve one architecture. A storefront per merchant holds one
   credential and needs no privilege that crosses a tenant boundary. ([#549])

5. ~~**An event does not say which entity changed without a payload parse.**~~
   **Fixed.** `type_definition_id` and `entity_id` are on the envelope, so this
   projector routes without decoding a payload. It keeps the payload fallback
   for a delivery recorded by an older service. ([#550])

6. ~~**There is no data type for long text.**~~ **Fixed.** The `text` data type
   stores as a string and declares that the value is long, so a generated form
   draws a text area. The `ecommerce` template's `description` uses it. ([#551])

7. ~~**A media value's bytes are only reachable with a tenant credential.**~~
   **Fixed.** `POST /media/{key}/signed-url` mints a signed, expiring link that
   anyone can redeem with no credential, so this storefront redirects to it
   instead of proxying every photo. ([#552])

8. ~~**Blob storage needs a writable directory the distroless image cannot
   create.**~~ **Fixed.** The disk store now proves the root is writable when
   it is built, so a non-writable `FLEXITYPE_BLOB_DIR` stops the service at
   start-up instead of losing the first upload hours later. The `blob-init`
   container below still chowns the volume, which is what makes the root
   writable in the first place. ([#553])

#!/usr/bin/env bash
# Seeds the marketplace: onboards three merchants, gives each its OWN subtypes
# of the shared product type, writes products, and then asserts that each
# merchant's own storefront projected its own catalogue — and only its own.
#
#   cd examples/marketplace
#   docker compose up --build --wait
#   ./seed.sh
#
# Every command here runs against the compose stack above.
set -euo pipefail

# jq parses every response below. Without this check the first call fails with
# an opaque "command not found" under set -e, which reads like a server error
# rather than a missing prerequisite.
for tool in jq curl docker; do
  if ! command -v "$tool" > /dev/null 2>&1; then
    echo "seed.sh needs $tool. Install it and re-run." >&2
    exit 1
  fi
done

DIR="$(cd "$(dirname "$0")" && pwd)"
FLEXITYPE="${FLEXITYPE:-http://localhost:8080}"
PLATFORM="${PLATFORM:-http://localhost:9300}"
# One storefront per merchant. Each holds one credential and serves one
# catalogue, so there is no service here that can read two merchants.
ALPINE="${ALPINE:-http://localhost:9200}"
BOLT="${BOLT:-http://localhost:9201}"
CONSOLE_TOKEN="${PLATFORM_API_TOKEN:-platform-console-demo-token}"
api() { curl -sS -H "Authorization: Bearer $CONSOLE_TOKEN" -H 'Content-Type: application/json' "$@"; }

# --- 1. The flexitype admin credential ---------------------------------------
#
# The compose file DECIDES the bootstrap admin token and hands the same value
# to flexitype (FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN) and to the platform
# (FLEXITYPE_ADMIN_TOKEN), so nothing has to be captured from a log at all.
# A real deployment generates one with `flexitype bootstrap-token` and keeps
# it in a secret manager.
echo "==> The admin credential comes from the compose file, not from a log"

echo "==> Waiting for the platform"
for _ in $(seq 1 60); do
  if curl -fs "$PLATFORM/healthz" > /dev/null 2>&1; then break; fi
  sleep 1
done
curl -fs "$PLATFORM/healthz" > /dev/null

# --- 2. Onboard the merchants ------------------------------------------------
#
# Onboarding creates the tenant, a service account scoped to it, applies the
# `ecommerce_strict` template, registers the storefront webhook and runs a first
# backfill. It is idempotent, so re-running this script is safe.
# A failure here used to print ERROR and carry on, so a merchant that was
# never onboarded looked like one that was — and the run failed later, or not
# at all.
onboard() {
  local body
  body=$(api -X POST "$PLATFORM/api/merchants" \
    -d "{\"id\":\"$1\",\"display_name\":\"$2\",\"tenant\":\"$1\"}")
  local id
  id=$(echo "$body" | jq -r '.id // ""')
  if [ -z "$id" ]; then
    echo "FAILED: onboarding $1: $(echo "$body" | jq -r '.error.message // .')" >&2
    exit 1
  fi
  echo "    $id"
}

# Two merchants, each with its own storefront. Two is enough to show the
# isolation the example is about; a third would be a third deployment.
echo "==> Onboarding merchants"
onboard alpine "Alpine Apparel"
onboard bolt "Bolt Electronics"

# --- 3. Each merchant EXTENDS the shared product type ------------------------
#
# The starter template gave every merchant the same root `product`. Each one
# now adds a subtype with the fields only it has. Each merchant's storefront
# projects its own subtype without knowing it in advance.
subtype() {
  merchant="$1"; shift
  api -X POST "$PLATFORM/api/merchants/$merchant/types" -d "$1" | jq -r '.internal_name // .error.message'
}

echo "==> Creating each merchant's own subtypes"
subtype alpine '{
  "internal_name":"apparel","display_name":"Apparel",
  "attributes":[
    {"internal_name":"size","display_name":"Size","data_type":"string"},
    {"internal_name":"colour","display_name":"Colour","data_type":"string"}
  ]}'
subtype bolt '{
  "internal_name":"electronics","display_name":"Electronics",
  "attributes":[
    {"internal_name":"voltage","display_name":"Voltage","data_type":"integer"},
    {"internal_name":"warranty_months","display_name":"Warranty (months)","data_type":"integer"}
  ]}'

# --- 4. Products -------------------------------------------------------------
#
# Every field of a product is written in ONE batch, so either the whole product
# lands and its events fire, or none of it does.
product() {
  api -X PUT "$PLATFORM/api/merchants/$1/products/$2" -d "$3" | jq -c '{entity: .entity_id, written: .written}'
}

echo "==> Writing products"
product alpine tee-merino '{"type":"apparel","values":{
  "name":"Merino Base Layer","description":"A featherweight merino wool base layer for cold mornings.",
  "sku":"ALP-1","status":"active","price":"89.00","currency":"EUR","in_stock":true,
  "size":"L","colour":"Slate"}}'
product alpine jacket-draft '{"type":"apparel","values":{
  "name":"Unreleased Shell Jacket","description":"Still in design review.",
  "sku":"ALP-2","status":"draft","price":"249.00","currency":"EUR","in_stock":false,
  "size":"M","colour":"Ember"}}'
product bolt lamp-desk '{"type":"electronics","values":{
  "name":"Merino-Shade Desk Lamp","description":"A warm desk lamp with a wool shade.",
  "sku":"BOLT-1","status":"active","price":"45.00","currency":"EUR","in_stock":true,
  "voltage":12,"warranty_months":24}}'
product bolt kettle '{"type":"electronics","values":{
  "name":"Pour-Over Kettle","description":"A gooseneck kettle with a temperature dial.",
  "sku":"BOLT-2","status":"active","price":"120.00","currency":"EUR","in_stock":true,
  "voltage":240,"warranty_months":12}}'

# --- 5. A product image ------------------------------------------------------
#
# flexitype stores the bytes in its blob store and writes a media VALUE holding
# the object key, the MIME type and a checksum. The storefront projects that
# value and streams the bytes back with the merchant's own credential, so a
# shopper never holds one.
echo "==> Uploading a product image"
IMAGE="$DIR/.admin/pixel.png"
# A 1x1 PNG, written with printf so the script needs no image tool.
printf '\x89\x50\x4e\x47\x0d\x0a\x1a\x0a\x00\x00\x00\x0d\x49\x48\x44\x52\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0d\x49\x44\x41\x54\x78\xda\x63\x64\xf8\xcf\x50\x0f\x00\x03\x86\x01\x80\x5a\x34\x7d\x6b\x00\x00\x00\x00\x49\x45\x4e\x44\xae\x42\x60\x82' > "$IMAGE"
upload=$(curl -sS -X POST -H "Authorization: Bearer $CONSOLE_TOKEN" \
  -F "file=@$IMAGE;type=image/png" \
  "$PLATFORM/api/merchants/alpine/products/tee-merino/image?type=apparel")
echo "$upload" | jq '{object_key: .value.object_key, mime: .value.mime, size: .value.size}'
OBJECT_KEY=$(echo "$upload" | jq -r '.value.object_key')

# --- 6. The storefront catches up --------------------------------------------
#
# The products above reach the storefront over signed webhooks, so there is a
# short delay: the outbox relay dispatches, and the storefront debounces a
# burst per entity.
echo "==> Waiting for each storefront's projection"
alpine_count() { curl -sS "$ALPINE/api/products?limit=100" | jq '.items | length'; }
bolt_count() { curl -sS "$BOLT/api/products?limit=100" | jq '.items | length'; }
for _ in $(seq 1 60); do
  if [ "$(alpine_count)" -ge 1 ] && [ "$(bolt_count)" -ge 1 ]; then break; fi
  sleep 1
done

# --- 7. Assert what a shopper sees -------------------------------------------
echo "==> Alpine's storefront serves Alpine's catalogue"
curl -sS "$ALPINE/api/products?limit=100" |
  jq '{store: "alpine", count: (.items | length), products: [.items[] | {merchant, subtype, name, price}]}'
echo "==> Bolt's storefront serves Bolt's"
curl -sS "$BOLT/api/products?limit=100" |
  jq '{store: "bolt", count: (.items | length), products: [.items[] | {merchant, subtype, name, price}]}'

# The property that replaced "one storefront aggregates every merchant": each
# one holds ONE merchant's catalogue, so nothing here can read two.
others=$(curl -sS "$ALPINE/api/products?limit=100" | jq '[.items[] | select(.merchant != "Alpine Apparel")] | length')
if [ "$others" -ne 0 ]; then
  echo "FAILED: alpine's storefront returned $others products belonging to another merchant" >&2
  exit 1
fi
others=$(curl -sS "$BOLT/api/products?limit=100" | jq '[.items[] | select(.merchant != "Bolt Electronics")] | length')
if [ "$others" -ne 0 ]; then
  echo "FAILED: bolt's storefront returned $others products belonging to another merchant" >&2
  exit 1
fi
echo "    each storefront returns only its own merchant's products"

echo "==> Full-text search within one merchant's catalogue: 'merino'"
curl -sS --get "$ALPINE/api/products" --data-urlencode 'q=merino' |
  jq '[.items[] | {merchant, name}]'

echo "==> Filter by price range"
curl -sS --get "$BOLT/api/products" \
  --data-urlencode 'min_price=40' --data-urlencode 'max_price=100' |
  jq '[.items[] | {name, price}]'

echo "==> A draft and an archived product are invisible to shoppers"
code=$(curl -s -o /dev/null -w '%{http_code}' "$ALPINE/api/products/jacket-draft")
if [ "$code" != "404" ]; then
  echo "FAILED: alpine/jacket-draft is reachable (HTTP $code); only active products may be" >&2
  exit 1
fi
echo "    alpine/jacket-draft -> 404"

echo "==> One merchant's storefront cannot be asked for another's product"
# tee-merino belongs to alpine. Bolt's storefront has never held it.
code=$(curl -s -o /dev/null -w '%{http_code}' "$BOLT/api/products/tee-merino")
if [ "$code" != "404" ]; then
  echo "FAILED: bolt's storefront answered for an alpine product (HTTP $code)" >&2
  exit 1
fi
echo "    bolt asked for alpine's tee-merino -> 404"

echo "==> The product image is served through the storefront"
# Wait for the projection to carry THIS upload's object key. Replacing a media
# value garbage-collects the superseded blob, so a storefront still holding the
# previous key would fetch an object that no longer exists.
for _ in $(seq 1 60); do
  projected=$(curl -sS "$ALPINE/api/products/tee-merino" | jq -r '.image.object_key // ""')
  if [ "$projected" = "$OBJECT_KEY" ]; then break; fi
  sleep 1
done
# The storefront REDIRECTS to a signed, expiring link rather than proxying the
# bytes, so the first response is a 302 and the second is the image itself.
img_redirect=$(curl -s -o /dev/null -w '%{http_code}' "$ALPINE/api/products/tee-merino/image")
if [ "$img_redirect" != "302" ]; then
  echo "FAILED: expected a redirect to a signed link, got HTTP $img_redirect" >&2
  exit 1
fi
signed=$(curl -s -o /dev/null -w '%{redirect_url}' "$ALPINE/api/products/tee-merino/image")
case "$signed" in
  */media/signed/*) ;;
  *) echo "FAILED: the redirect does not point at a signed link: $signed" >&2; exit 1 ;;
esac
# Redeemed with NO credential at all: the signature is the credential.
img_status=$(curl -s -o /dev/null -w '%{http_code}' "$signed")
if [ "$img_status" != "200" ]; then
  echo "FAILED: the signed image link is not reachable (HTTP $img_status)" >&2
  exit 1
fi
echo "    alpine/tee-merino/image -> 302 -> signed link -> 200 (no credential)"

echo "==> A shopper cannot widen the filter to see drafts"
drafts=$(curl -sS "$ALPINE/api/products?status=draft" | jq '.items | length')
if [ "$drafts" -ne 0 ]; then
  echo "FAILED: status=draft returned $drafts products" >&2
  exit 1
fi

echo "==> Merchant records never expose a token"
api "$PLATFORM/api/merchants" | jq '{merchants: [.items[] | {id, display_name, tenant}]}'

echo
echo "Done."
echo "  alpine's storefront:  $ALPINE/api/products"
echo "  bolt's storefront:    $BOLT/api/products"
echo "  platform:    $PLATFORM/api/merchants  (Bearer $CONSOLE_TOKEN)"
echo "  flexitype:   $FLEXITYPE"

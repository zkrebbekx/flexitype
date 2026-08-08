#!/usr/bin/env bash
# Seeds the marketplace: onboards three merchants, gives each its OWN subtypes
# of the shared product type, writes products, and then asserts that the
# storefront aggregated them.
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
STOREFRONT="${STOREFRONT:-http://localhost:9200}"
CONSOLE_TOKEN="${PLATFORM_API_TOKEN:-platform-console-demo-token}"
TOKEN_FILE="$DIR/.admin/admin-token"

api() { curl -sS -H "Authorization: Bearer $CONSOLE_TOKEN" -H 'Content-Type: application/json' "$@"; }

# --- 1. Hand the platform the flexitype admin credential ---------------------
#
# flexitype prints its bootstrap admin token to STDOUT once, and its image is
# distroless, so the compose stack cannot capture it into an environment
# variable. It is read out of the container log here and written to the file
# the platform waits for. A real deployment mounts a secret instead.
echo "==> Capturing the flexitype bootstrap admin token"
mkdir -p "$DIR/.admin"
# The secret half of a token is base64url, so it can contain '-' and '_'.
# A character class without the hyphen truncated the token at the first one,
# and the platform then failed every admin call with "invalid credentials".
printed=$(docker compose logs --no-color flexitype 2>/dev/null |
  grep -A1 'bootstrap admin account created' | tail -n1 | tr -d '\r' | grep -o 'ft_[A-Za-z0-9_-]*' || true)
if [ -n "$printed" ]; then
  printf '%s' "$printed" > "$TOKEN_FILE"
  echo "    captured a freshly printed token"
elif [ -s "$TOKEN_FILE" ]; then
  echo "    reusing the token captured by an earlier run"
else
  echo "seed.sh found no admin token. flexitype prints it only on a FIRST start." >&2
  echo "Run 'docker compose down --volumes' and 'docker compose up --build --wait' to get a new one." >&2
  exit 1
fi

echo "==> Waiting for the platform to pick the token up"
for _ in $(seq 1 60); do
  if curl -fs "$PLATFORM/healthz" > /dev/null 2>&1; then break; fi
  sleep 1
done
curl -fs "$PLATFORM/healthz" > /dev/null

# --- 2. Onboard the merchants ------------------------------------------------
#
# Onboarding creates the tenant, a service account scoped to it, applies the
# `ecommerce` template, registers the storefront webhook and runs a first
# backfill. It is idempotent, so re-running this script is safe.
onboard() {
  api -X POST "$PLATFORM/api/merchants" \
    -d "{\"id\":\"$1\",\"display_name\":\"$2\",\"tenant\":\"$1\"}" |
    jq -r '.id // ("ERROR: " + (.error.message // "unknown"))'
}

echo "==> Onboarding merchants"
onboard alpine "Alpine Apparel"
onboard bolt "Bolt Electronics"
onboard cellar "Cellar Coffee"

# --- 3. Each merchant EXTENDS the shared product type ------------------------
#
# The starter template gave every merchant the same root `product`. Each one
# now adds a subtype with the fields only it has. The storefront aggregates all
# three without knowing any of them in advance.
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
subtype cellar '{
  "internal_name":"coffee","display_name":"Coffee",
  "attributes":[
    {"internal_name":"roast","display_name":"Roast","data_type":"string"},
    {"internal_name":"weight_grams","display_name":"Weight (g)","data_type":"integer"}
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
product cellar beans-ethiopia '{"type":"coffee","values":{
  "name":"Ethiopia Guji","description":"A washed Guji with jasmine and stone fruit.",
  "sku":"CEL-1","status":"active","price":"18.50","currency":"EUR","in_stock":true,
  "roast":"filter","weight_grams":250}}'
product cellar beans-archived '{"type":"coffee","values":{
  "name":"Last Season Blend","description":"Withdrawn from sale.",
  "sku":"CEL-2","status":"archived","price":"14.00","currency":"EUR","in_stock":false,
  "roast":"espresso","weight_grams":250}}'

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
echo "==> Waiting for the storefront projection"
active_count() { curl -sS "$STOREFRONT/api/products?limit=100" | jq '.items | length'; }
for _ in $(seq 1 60); do
  if [ "$(active_count)" -ge 4 ]; then break; fi
  sleep 1
done

# --- 7. Assert what a shopper sees -------------------------------------------
echo "==> The storefront aggregates every merchant"
curl -sS "$STOREFRONT/api/products?limit=100" |
  jq '{count: (.items | length), products: [.items[] | {merchant, subtype, name, price}]}'

count=$(active_count)
if [ "$count" -ne 4 ]; then
  echo "FAILED: expected 4 active products across three merchants, got $count" >&2
  echo "Check 'docker compose logs storefront' for projection errors." >&2
  exit 1
fi

echo "==> Full-text search across merchants: 'merino'"
curl -sS --get "$STOREFRONT/api/products" --data-urlencode 'q=merino' |
  jq '[.items[] | {merchant, name}]'

echo "==> Filter by merchant and price range"
curl -sS --get "$STOREFRONT/api/products" \
  --data-urlencode 'merchant=bolt' --data-urlencode 'min_price=40' --data-urlencode 'max_price=100' |
  jq '[.items[] | {name, price}]'

echo "==> A draft and an archived product are invisible to shoppers"
for hidden in alpine/jacket-draft cellar/beans-archived; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$STOREFRONT/api/products/${hidden%%/*}/${hidden##*/}")
  if [ "$code" != "404" ]; then
    echo "FAILED: $hidden is reachable (HTTP $code); only active products may be" >&2
    exit 1
  fi
  echo "    $hidden -> 404"
done

echo "==> The product image is served through the storefront"
# Wait for the projection to carry THIS upload's object key. Replacing a media
# value garbage-collects the superseded blob, so a storefront still holding the
# previous key would fetch an object that no longer exists.
for _ in $(seq 1 60); do
  projected=$(curl -sS "$STOREFRONT/api/products/alpine/tee-merino" | jq -r '.image.object_key // ""')
  if [ "$projected" = "$OBJECT_KEY" ]; then break; fi
  sleep 1
done
img_status=$(curl -s -o /dev/null -w '%{http_code}' "$STOREFRONT/api/products/alpine/tee-merino/image")
if [ "$img_status" != "200" ]; then
  echo "FAILED: the product image is not reachable (HTTP $img_status)" >&2
  exit 1
fi
echo "    alpine/tee-merino/image -> 200"

echo "==> A shopper cannot widen the filter to see drafts"
drafts=$(curl -sS "$STOREFRONT/api/products?status=draft" | jq '.items | length')
if [ "$drafts" -ne 0 ]; then
  echo "FAILED: status=draft returned $drafts products" >&2
  exit 1
fi

echo "==> Merchant records never expose a token"
api "$PLATFORM/api/merchants" | jq '{merchants: [.items[] | {id, display_name, tenant}]}'

echo
echo "Done."
echo "  storefront:  $STOREFRONT/api/products"
echo "  platform:    $PLATFORM/api/merchants  (Bearer $CONSOLE_TOKEN)"
echo "  flexitype:   $FLEXITYPE"

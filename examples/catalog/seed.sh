#!/usr/bin/env bash
# Seeds the product-catalog schema and a few products, then runs a couple of
# FQL queries — the scripted version of the walkthrough in README.md.
#
#   BASE=http://localhost:8080 ./seed.sh
#
# Set TOKEN to a service-account token if the target requires auth.
set -euo pipefail

BASE="${BASE:-http://localhost:8080}"
API="$BASE/api/v1"
DIR="$(cd "$(dirname "$0")" && pwd)"

req() {
  if [ -n "${TOKEN:-}" ]; then
    curl -sS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"
  else
    curl -sS -H 'Content-Type: application/json' "$@"
  fi
}

echo "==> Importing the catalog schema (idempotent)"
req -X POST --data-binary @"$DIR/schema.json" "$API/schema/import" | jq .

# Resolve the type ids we just imported (import keys by internal name).
type_id() { req "$API/type-definitions?internal_name=$1" | jq -r '.items[0].id'; }
# effective-attributes includes inherited ones (name/sku/status live on the
# base "product" type but are part of every subtype's schema).
attr_id() {
  req "$API/type-definitions/$1/effective-attributes" |
    jq -r ".items[] | select(.attribute.internal_name==\"$2\") | .attribute.id"
}

BOOK=$(type_id book)
BOOK_NAME=$(attr_id "$BOOK" name)
BOOK_SKU=$(attr_id "$BOOK" sku)
BOOK_STATUS=$(attr_id "$BOOK" status)
BOOK_AUTHOR=$(attr_id "$BOOK" author)
BOOK_PAGES=$(attr_id "$BOOK" pages)

echo "==> Creating a book (entity 'book-dune') with a batch value write"
req -X POST "$API/values/batch" -d "{\"items\":[
  {\"attribute_definition_id\":\"$BOOK_NAME\",\"entity_id\":\"book-dune\",\"type_definition_id\":\"$BOOK\",\"value\":\"Dune\"},
  {\"attribute_definition_id\":\"$BOOK_SKU\",\"entity_id\":\"book-dune\",\"type_definition_id\":\"$BOOK\",\"value\":\"BK-DUNE-01\"},
  {\"attribute_definition_id\":\"$BOOK_STATUS\",\"entity_id\":\"book-dune\",\"type_definition_id\":\"$BOOK\",\"value\":\"active\"},
  {\"attribute_definition_id\":\"$BOOK_AUTHOR\",\"entity_id\":\"book-dune\",\"type_definition_id\":\"$BOOK\",\"value\":\"Frank Herbert\"},
  {\"attribute_definition_id\":\"$BOOK_PAGES\",\"entity_id\":\"book-dune\",\"type_definition_id\":\"$BOOK\",\"value\":412}
]}" | jq '{written: (.items | length)}'

echo "==> FQL: books with more than 300 pages"
req --get "$API/query" --data-urlencode "type=book" --data-urlencode "q=pages > 300" | jq '{matches: [.items[].entity_id]}'

echo "==> FQL: active products across the whole hierarchy"
req --get "$API/query" --data-urlencode "type=product" --data-urlencode 'q=status = "active"' | jq '{matches: [.items[].entity_id]}'

echo "==> Done. Explore the console at $BASE"

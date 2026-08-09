#!/usr/bin/env bash
# Seeds a small kitchen and then proves, with curl, that the SERVICE does the
# costing: a supplier price list lands, and every dish recosts itself.
#
# Safe to re-run.
set -euo pipefail

for tool in curl jq; do
  if ! command -v "$tool" > /dev/null 2>&1; then
    echo "seed.sh needs $tool" >&2
    exit 1
  fi
done

KITCHEN="${KITCHEN:-http://localhost:9400}"
api() { curl -sS -H 'Content-Type: application/json' "$@"; }

echo "==> Waiting for the kitchen"
for _ in $(seq 1 60); do
  if curl -fs "$KITCHEN/healthz" > /dev/null 2>&1; then break; fi
  sleep 1
done
curl -fs "$KITCHEN/healthz" > /dev/null

echo "==> Ingredients, bought in the units the suppliers sell"
# A pound of butter, a kilo of flour, 500 g of chocolate. The service converts.
api -X PUT "$KITCHEN/api/ingredients/flour" \
  -d '{"name":"Flour","supplier":"Mills","pack_size":{"magnitude":"1","unit":"kg"},"pack_price":"1.20"}' > /dev/null
api -X PUT "$KITCHEN/api/ingredients/butter" \
  -d '{"name":"Butter","supplier":"Dairy","pack_size":{"magnitude":"1","unit":"lb"},"pack_price":"3.40"}' > /dev/null
api -X PUT "$KITCHEN/api/ingredients/chocolate" \
  -d '{"name":"Dark chocolate","supplier":"Cocoa Co","pack_size":{"magnitude":"500","unit":"g"},"pack_price":"6.00"}' > /dev/null

api "$KITCHEN/api/ingredients" | jq '[.items[] | {id, pack: (.pack_size.magnitude + " " + .pack_size.unit), pack_price, cost_per_kg}]'

echo "==> A dish, priced per channel and named per locale"
api -X PUT "$KITCHEN/api/dishes/tart" -d '{
  "course":"dessert",
  "name":{"":"Chocolate tart","fr":"Tarte au chocolat"},
  "description":{"":"A dark chocolate tart on a shortcrust base."},
  "price":{"dine_in":"8.50","delivery":"9.50","catering":"7.00"}}' > /dev/null

echo "==> Its recipe, in grams"
api -X PUT "$KITCHEN/api/dishes/tart/lines/l-flour" \
  -d '{"ingredient_id":"flour","quantity":{"magnitude":"250","unit":"g"}}' > /dev/null
api -X PUT "$KITCHEN/api/dishes/tart/lines/l-butter" \
  -d '{"ingredient_id":"butter","quantity":{"magnitude":"150","unit":"g"}}' > /dev/null
api -X PUT "$KITCHEN/api/dishes/tart/lines/l-choc" \
  -d '{"ingredient_id":"chocolate","quantity":{"magnitude":"200","unit":"g"}}' > /dev/null

before=$(api "$KITCHEN/api/dishes/tart" | jq -r '.food_cost')
echo "    food cost: $before"
api "$KITCHEN/api/dishes/tart" | jq '{food_cost, line_count, margin, lines: [.lines[] | {ingredient, qty: (.quantity.magnitude + " " + .quantity.unit), cost_per_kg, line_cost}]}'

# Re-running must show the same story, so the demo sets its own starting
# point rather than assuming the price is still what the first run left.
api -X PUT "$KITCHEN/api/ingredients/chocolate" \
  -d '{"name":"Dark chocolate","supplier":"Cocoa Co","pack_size":{"magnitude":"500","unit":"g"},"pack_price":"6.00"}' > /dev/null
before=$(api "$KITCHEN/api/dishes/tart" | jq -r '.food_cost')

echo "==> A supplier price list arrives (chocolate 6.00 -> 9.00 for 500 g)"
# ONE value per ingredient is written. Nothing here recomputes anything.
printf 'id,name,supplier,pack_size,pack_unit,pack_price\nchocolate,Dark chocolate,Cocoa Co,500,g,9.00\n' |
  curl -sS -X POST --data-binary @- "$KITCHEN/api/ingredients/import" | jq

after=$(api "$KITCHEN/api/dishes/tart" | jq -r '.food_cost')
echo "    food cost: $before -> $after"
if [ "$before" = "$after" ]; then
  echo "FAILED: the dish did not recost itself" >&2
  exit 1
fi
api "$KITCHEN/api/dishes/tart" | jq '{food_cost, margin}'

echo "==> A dish reaches the menu only when it is complete"
# On its own dish, withdrawn and rebuilt each run, so the gate is demonstrated
# every time rather than only on a clean database.
curl -sS -X DELETE "$KITCHEN/api/dishes/gate-demo" > /dev/null
api -X PUT "$KITCHEN/api/dishes/gate-demo" \
  -d '{"name":{"":"Almond financier"},"course":"dessert","contains_allergens":true,
       "price":{"dine_in":"4.50"}}' > /dev/null

blocked=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$KITCHEN/api/dishes/gate-demo/publish")
if [ "$blocked" != "422" ]; then
  echo "FAILED: a dish with undeclared allergens was publishable (HTTP $blocked)" >&2
  exit 1
fi
curl -sS -X POST "$KITCHEN/api/dishes/gate-demo/publish" | jq '{missing, score}'

api -X PUT "$KITCHEN/api/dishes/gate-demo" -d '{"allergens":["nuts","egg"]}' > /dev/null
api -X POST "$KITCHEN/api/dishes/gate-demo/publish" | jq '{id, status, allergens}'

echo "==> Next week's prices, staged and scheduled"
api -X POST "$KITCHEN/api/menu-changes" -d '{
  "name":"Autumn prices",
  "publish_at":"2099-10-01T06:00:00Z",
  "prices":{"tart":{"dine_in":"9.50","delivery":"10.50"}}}' | jq '{name, state, publish_at}'

echo "    today's price is unchanged:"
api "$KITCHEN/api/dishes/tart" | jq '.price'

echo
echo "Done."
echo "  kitchen console:  http://localhost:8083"
echo "  the API:          $KITCHEN/api/dishes"
echo "  flexitype:        http://localhost:8080"

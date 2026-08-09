# Example: a kitchen that costs its own recipes

A restaurant group's recipe and menu system. **Single tenant on purpose** —
nothing here is about tenancy (see [`../marketplace`](../marketplace/) for
that). The point of this one is what the service computes on its own.

```
                    ┌───────────── kitchen ─────────────┐
   chef, console ──►│ ingredients, dishes, recipe lines │
                    │ price matrix, menu changes        │──► flexitype
                    │                                   │    (one tenant)
                    │ NOTHING here adds up a price      │
                    └───────────────────────────────────┘
```

**Prerequisites:** Docker, `curl` and `jq`. `seed.sh` checks for them.

```bash
cd examples/kitchen
docker compose up --build --wait
./seed.sh
open http://localhost:8083          # the kitchen console
```

## The claim

The costing is entirely the service's. This application writes an ingredient's
pack price and a recipe line's quantity, and nothing else:

```
ingredient.cost_per_kg        formula   pack_price / pack_size
recipe_line.ingredient_cost   rollup    sum(parent(of_ingredient).cost_per_kg)
recipe_line.line_cost         formula   quantity * ingredient_cost
dish.food_cost                rollup    sum(child(has_line).line_cost)
dish.line_count               rollup    count(child(has_line))
```

A supplier raises a price, and every dish that uses that ingredient recosts
itself **two relationships away**. `seed.sh` proves it in one call:

```bash
printf 'id,name,supplier,pack_size,pack_unit,pack_price\nchocolate,Dark chocolate,Cocoa Co,500,g,9.00\n' |
  curl -sS -X POST --data-binary @- http://localhost:9400/api/ingredients/import
#    food cost: 3.82435753714287569756 -> 5.02435753714287576417
```

That import writes **one value per ingredient**. Search this example for the
arithmetic that produced the new dish cost — there is none.

## What each feature is doing here

| Feature | Why the kitchen needs it |
| --- | --- |
| **Unit families** | Flour is bought by the kilo, butter by the pound, chocolate in 500 g bars, and every recipe is in grams. Costing IS unit conversion. A `quantity` keeps the unit it was entered in and compares in the family's base unit, so one cost-per-kilogram serves all of them. |
| **Computed formulas** | `pack_price / pack_size` is a price per kilogram whatever the invoice said. |
| **Computed rollups** | A dish's cost is the total of its lines; a line's ingredient cost is reached *through the link* to the ingredient. Change either end and the total follows. |
| **Relationships** | `dish --has_line--> recipe_line --of_ingredient--> ingredient`. Unlink a line and the dish's cost drops — with no value written to the dish. |
| **Scoped values** | One dish, three prices: a table, a delivery app and a catering order. `price` is scoped by channel, so it is one attribute, not three. |
| **Localized values** | `name` and `description` per locale, for a group operating in more than one country. |
| **Dependencies** | A dish that declares it contains allergens must list them. |
| **Completeness** | What decides a dish is ready for the menu — read from the service, not re-derived here. |
| **Change sets** | Next week's prices, approved now and published at 06:00 on Monday, in one transaction. A menu is never half-changed. |
| **Revisions / as-of** | What a dish cost in January. Not reconstructable from today's prices. |

## Three things worth reading the code for

### A rollup's inputs are somewhere else

`dish.food_cost` reads nothing on the dish. Its inputs are on the recipe lines,
which are different entities — so **no value event fires for the dish** when a
line changes. Two triggers cover that, and both are the service's:

- linking or unlinking recomputes both endpoints;
- writing a value recomputes whatever aggregates that entity.

That is why removing a line changes the dish's cost even though nothing wrote
to the dish. `DELETE /api/dishes/{id}/lines/{lineID}` unlinks and then simply
*re-reads* the dish.

### A scope is an address, not a closed set

`price` is scoped by channel, and flexitype will accept **any** channel string:
a scope is part of a value's address, not an enumeration. Write
`price` in channel `dinein` and it is stored happily — and read by nothing,
because every read path here iterates the channels the menu knows.

So the API refuses a channel or locale the model does not declare, and says
which ones exist. The alternative is a typo that silently prices a dish for
nobody.

### The one number this example computes itself

Margin — `(price - food_cost) / price` — is computed in
[`api/margin.go`](api/margin.go), and the reason is worth stating.

A formula reads the **base scope** of its inputs. `price` is scoped by channel,
so it has three values and no single base one. `(price - food_cost) / price`
therefore has three answers, and the service **refuses to pretend it has one**:
a formula that reads a scopable attribute is rejected when it is defined.

So the margin is computed per channel, by the caller, from two values the
service does provide. That refusal is the feature working: the alternative is a
number that silently tracks one channel.

### A dependency describes; publishing decides

`allergens` is required once `contains_allergens` is true, and the rule
declares `"enforce": "on_read"`. Setting the flag alone is **accepted**: a chef
ticks the box and then types the list, in that order, so refusing the tick
would make the form unusable. The rule describes what the dish needs, not the
order someone fills it in.

That is the default, and it is stated in
[`api/schema.go`](api/schema.go) because it is a choice. The other mode,
`"enforce": "on_write"`, refuses the write instead — see
[docs/dependencies.md](../../docs/dependencies.md#enforcing-a-requirement) for
when each one fits.

Something has to turn "needs" into "must", and here that is publishing:
`POST /api/dishes/{id}/publish` reads the service's own **completeness** report
and refuses while anything required is missing. A dependency added later is
enforced with no change to that code.

```bash
curl -sX POST localhost:9400/api/dishes/gate-demo/publish | jq
# {"error":{"message":"this dish is not ready for the menu: allergens"},
#  "missing":["allergens"],"score":0.5}
```

## A rounding note

A `quantity`'s base magnitude is a float, so a cost derived by **dividing by**
one carries the float's tail: a pound pack at 3.40 gives
`7.49571691428583737293` per kg, not a tidy 7.4957. The money values are
decimals and stay exact; it is the conversion that is inexact. A kitchen
rounds when it prints a menu, which is why the console renders to two places
and never parses a decimal into a float.

## The API

```bash
GET    /api/ingredients
PUT    /api/ingredients/{id}
POST   /api/ingredients/import         # a supplier CSV: id,name,supplier,pack_size,pack_unit,pack_price

GET    /api/dishes
GET    /api/dishes/{id}                # with lines, costs, prices and margins
PUT    /api/dishes/{id}
PUT    /api/dishes/{id}/lines/{lineID} # writes the quantity and links the line
DELETE /api/dishes/{id}/lines/{lineID}
POST   /api/dishes/{id}/publish        # gated on completeness

POST   /api/menu-changes               # stage prices, optionally scheduled
GET    /api/menu-changes
GET    /api/dishes/{id}/cost-history   # what it cost at each revision
```

## Tests

```bash
(cd api && go test ./...)
(cd console && npm ci && npm test)
```

The Go suite drives a **real** flexitype — an in-memory one, which runs the
same materializer, unit families and change sets as the Postgres-backed
service. Every cost it asserts is the service's own:

- a dish's food cost is the total of its lines, and each line's cost is its
  quantity times a cost reached through a link;
- a supplier price list moves the dish's cost two relationships away;
- removing a line changes the total with nothing written to the dish;
- a pound pack and a 500 g pack both land on a cost per kilogram;
- one attribute holds a price per channel and a name per locale;
- a dish with undeclared allergens cannot be published, and can once they are
  listed;
- a scheduled price change waits, approved, while today's price is unchanged;
  a published one moves every price in it together.

The console's suite pins that the page **reads** costs rather than computing
them, that a quantity edit sends the quantity and no cost at all, and that
money is never parsed into a float.

## What this example does not do

- **No sub-recipes.** A sauce used by three dishes would be a second rollup
  layer (`dish -> sub_recipe -> line`). The engine handles the chain; the
  example keeps one level so the model stays readable.
- **No yield or waste factors.** A real kitchen costs 1 kg of onions as
  0.85 kg usable. That is one more formula on the line.
- **No stock or purchasing.** Costing is not inventory.

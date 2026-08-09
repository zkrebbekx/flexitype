# Computed attributes

A computed attribute derives its value from a formula over the entity's other
attributes. It is materialized by an event subscriber, so the result is an
ordinary attribute value: queryable in FQL, present in exports, counted toward
completeness.

```json
{"kind": "formula", "formula": "(price - cost) / price"}
```

A **rollup** derives it from the entities a relationship reaches instead:

```json
{"kind": "rollup",
 "rollup": {"relationship": "has_line", "direction": "child",
            "aggregate": "sum", "target": "line_cost"}}
```

## Rollups

A rollup aggregates ONE attribute across the entities one relationship
reaches. Where a formula reads the entity's own values, a rollup reads its
neighbours'.

| Field | Meaning |
| --- | --- |
| `relationship` | The relationship definition's internal name. |
| `direction` | `child` (the entities below this one), `parent` (above), or `linked` (either side). |
| `aggregate` | `count`, `sum`, `min` or `max`. |
| `target` | The attribute aggregated on the far side. `count` needs none. |

```
ingredient.cost_per_kg     plain
recipe_line.ingredient_cost  rollup   sum(parent(of_ingredient).cost_per_kg)
recipe_line.line_cost        formula  quantity_kg * ingredient_cost
dish.food_cost               rollup   sum(child(has_line).line_cost)
```

A supplier price rises, and the dish's cost follows two relationships away.
Nothing writes to the line or to the dish: each rollup is recomputed because
something it reads changed.

### What triggers a recompute

A rollup's inputs are on OTHER entities, so the entity holding it receives no
value event of its own. Two triggers cover that:

- **A link changes.** Linking, unlinking or re-pinning recomputes both
  endpoints, so a total follows the links even though no value moved.
- **A counterpart's value changes.** The entities aggregating the written one
  are recomputed too, and their own writes cascade further — which is what
  carries a change along a chain of rollups and formulas.

Convergence is the same as for a formula: re-setting an unchanged value emits
no event, so the cascade stops. A cascade is bounded at eight hops, for the
case where convergence does not hold — a cycle of aggregates has to stop
rather than run for ever.

### What a rollup answers

| Aggregate | No counterparts | A counterpart with no value |
| --- | --- | --- |
| `count` | **0** | counted |
| `sum`, `min`, `max` | **undefined** — the value is cleared | skipped |

`count` answers 0 because "this dish has no lines" is a fact. `sum` of nothing
is not 0: reporting one would read as a free dish.

Only the BASE scope is aggregated. A localized or scoped member would
otherwise be counted once per locale, and a total would grow with the number
of translations.

### A rollup that could never work is refused

Three ways to write one that aggregates an empty set for ever — which looks
exactly like "no data yet":

- the relationship does not exist;
- the direction points the wrong way, so this type is never on the side the
  traversal starts from;
- the target does not exist on the type the relationship reaches, or is not
  numeric.

Each is a validation error when the attribute is created or updated. Rollups
were withheld for a release precisely because an unmaterialized one is
invisible; a mistyped one must not reintroduce that.

## The formula language

```
expr   = term (('+' | '-') term)*
term   = factor (('*' | '/') factor)*
factor = number | call | name | '(' expr ')' | '-' factor
call   = ('sum' | 'count' | 'avg' | 'min' | 'max') '(' name ')'
```

A name is an attribute's internal name, resolved against the entity's own
type and everything it inherits.

## A name carries every value, and a bare name needs exactly one

Evaluation reads **all** of an entity's values for a name. A bare name must
resolve to exactly one:

| Values for the name | `price * 2` | `sum(price)` |
| --- | --- | --- |
| none | undefined | undefined |
| one | computed | computed |
| several | **undefined** | computed |

That is why **a formula may not read a multi-valued attribute bare**, and the
definition is refused at write time rather than producing a number later. An
earlier version took whichever member the repository returned last: adding a
member changed the answer with no change to the schema or the formula, while
the computed attribute stayed populated and queryable, so nothing signalled
that the number tracked nothing.

The same rule works in reverse: an attribute a formula reads **bare** cannot
be made multi-valued. One that is only ever aggregated can, because the
aggregate already asked for every member. Becoming **localizable or scopable**
is refused for either kind of reader, aggregates included, because evaluation
reads the base scope and would drop every scoped member.

The reverse rule applies on **create** as well as update. Writing
`total = line_total * 2` before `line_total` exists is accepted — the
reference is unresolved — so creating `line_total` afterwards as multi-valued,
localizable or scopable is refused at that point.

## Aggregates

| Call | Meaning | No values at all |
| --- | --- | --- |
| `sum(x)` | total of every value | **undefined** |
| `count(x)` | how many values | **0** |
| `avg(x)` | mean | undefined |
| `min(x)` | smallest | undefined |
| `max(x)` | largest | undefined |

`count` answers 0 for an absent attribute because counting nothing has one
sensible reading. The others are **undefined**, which clears the computed
value: the entity holds no data for that attribute, so the total is unknown
rather than nought. A schema author who wants a zero can write
`sum(x) + 0 * count(x)`… and should not have to, so if that reading is wrong
for your model, say so on an issue rather than working around it.

The argument is a single name. An aggregate folds one attribute's values;
allowing an expression there would raise the question of what it means to fold
a computation over members that need not correspond to each other.

### Source data types

`sum`, `avg`, `min` and `max` fold **numbers**, so their source must be a
`bool`, `integer`, `float` or `decimal` attribute. A formula that folds or
reads any other type is refused at write time.

`count` folds **members**, so it accepts a source of any data type:
`count(tags)` over multi-valued strings and `count(photos)` over multi-valued
media both report how many values the entity holds.

The names `sum`, `count`, `avg`, `min` and `max` are not reserved. They are
aggregates only when a `(` follows, so an attribute called `count` still reads
bare in `count * 2`.

## Scoped attributes are refused

A formula cannot read a **localizable or scopable** attribute at all, even
inside an aggregate. Evaluation reads the base scope, and folding values that
mean different things in different locales is not an aggregate anyone asked
for. Model it explicitly instead: a computed attribute per locale, over
per-locale sources.

## Exactness

A `decimal` or `integer` target is evaluated in exact rational arithmetic,
aggregates included, so `sum` of `0.1` and `0.2` stores `0.3` rather than
`0.30000000000000004`, and `sum` of `9007199254740993` and `1` stores
`9007199254740994` rather than a value narrowed through `float64`. A result
outside `int64` clears the value rather than storing a wrong one. A `float`
target evaluates in `float64`, which is what choosing that type asks for.

## Rollups across relationships

Not supported. `sum(x)` folds one entity's own values; there is no syntax for
"sum this attribute across every child entity". It is a real gap rather than
an oversight — the cost is a dependency graph that spans entities, and an
invalidation story for it — and it is tracked rather than half-implemented.

## When the formula becomes undefined

A formula that stops producing a value — a missing input, a division by zero,
an aggregate over an absent attribute — **clears** the computed value. It does
not keep the last good one: a stale number with no formula behind it is
queryable in FQL, present in exports and counted toward completeness, with
nothing to explain where it came from.

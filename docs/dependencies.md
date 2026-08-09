# Attribute dependencies

A dependency is a conditional rule: when a **source** attribute's value matches
every condition, an **effect** applies to a **target** attribute. It is how a
schema says "a hazardous product needs a disposal plan" without a second type,
a nullable column or a validator in the host.

```json
{
  "source_attribute_id": "<contains_allergens>",
  "target_attribute_id": "<allergens>",
  "conditions": [{"kind": "equals", "value": {"type": "bool", "value": true}}],
  "effect": {"required": true}
}
```

An effect does one or more of three things:

| Effect | What it does |
| --- | --- |
| `allowed_values` | Narrows the target to a set. Several matched rules **intersect**. |
| `constraints` | Adds rules to the target's values. Several matched rules accumulate. |
| `required` | Overrides the target's own required flag, in either direction. |

## Enforcing a requirement

`required` is the only effect whose subject can be **absent**. Allowed values
and constraints are checked against a value the caller submitted, so there is
nothing to decide about when: they apply to that write. A demand for a value
that is not there has to be answered somewhere else, and `enforce` says where.

```json
{"required": true, "enforce": "on_write"}
```

| Mode | Behaviour |
| --- | --- |
| `on_read` (default) | The write is accepted. The gap is reported by the effective schema and by completeness, and the caller decides where to gate on it. |
| `on_write` | The write is refused while the value is missing. |

### Choosing a mode

Look at the **condition**, not the target.

| The condition is… | Mode | Why |
| --- | --- | --- |
| A **lifecycle state** — `status = active`, published, approved | `on_write` | Entering the state is a decision. A decision taken against an incomplete record is the thing worth refusing. |
| A **fact being entered** — `contains_allergens = true`, `hazardous = yes` | `on_read` | The fact and what it demands arrive in that order. Refusing the fact refuses the truth for being early. |

`on_read` is the default because most conditions are facts rather than states.

Two shipped templates make the same schema available either way, so the choice
is one you apply rather than one you edit:

| Template | Its two `status = active` rules | Choose it when |
| --- | --- | --- |
| `ecommerce` | report the gap | Products are assembled field by field, and something downstream decides when one goes live. |
| `ecommerce_strict` | refuse the write | Nothing downstream gates, so a product must never be stored active and unsellable. |

They are otherwise identical, and a test pins that: if they drift, the pair
stops demonstrating one decision and becomes two schemas to maintain.

Both shipped examples take a side, for the reason above:

- [`examples/marketplace`](../examples/marketplace/) applies
  `ecommerce_strict`. It writes a whole product in one batch, which is exactly
  how a caller satisfies an `on_write` rule.
- [`examples/kitchen`](../examples/kitchen/) reports: a chef ticks "contains
  allergens", then types the list, and publishing turns the gap into a refusal.

Before switching a rule to `on_write`, read
[Adding a blocking rule to data that already exists](#adding-a-blocking-rule-to-data-that-already-exists).

### on_read: the rule describes, something else decides

This is the default, and it is what every dependency did before `enforce`
existed. Setting `contains_allergens` to `true` on a dish with no allergens is
accepted, and the dish is then reported incomplete:

```bash
curl -s localhost:8080/api/v1/entities/<dish-type-id>/tart/completeness
# {"required":1,"filled":0,"score":0,
#  "missing":[{"internal_name":"allergens", ...}]}
```

Use it when a record is filled in over several steps and has to be allowed to
be incomplete in between — a draft, a wizard, a form someone saves and comes
back to. The host turns the report into a refusal at the point that matters:
publishing a dish, activating a product, submitting a claim. A dependency added
later is enforced with no change to that code.

### on_write: the rule refuses

```json
{"required": true, "enforce": "on_write"}
```

```bash
curl -s -X POST localhost:8080/api/v1/values -d '{"attribute_definition_id":"<contains_allergens>","entity_id":"tart","value":true}'
# 422 {"error":{"code":"DEPENDENCY_VIOLATION",
#      "message":"an attribute dependency requires a value for \"allergens\""}}
```

Use it when an entity must never be **stored** in the state the rule describes
as incomplete.

Three properties are worth knowing before you choose it.

**The check runs at the end of the write transaction, not on each value.** A
batch or an import row that supplies the condition and the required attribute
together passes whatever order its cells are written in. This matters because a
CSV column order is not a schema decision: `contains_allergens` usually lands
before `allergens`, and a per-value check would refuse a row whose finished
state is complete.

```bash
# Accepted: one row, one transaction, both cells.
id,contains_allergens,allergens
tart,true,gluten

# Refused: the row is complete in neither order.
id,contains_allergens,allergens
tart,true,
```

Best-effort import chunks on **entity** boundaries, never mid-entity, and
retries a failed chunk one entity at a time. So a refused entity does not cost
the others, and a file's outcome does not depend on where its rows happen to
fall relative to a chunk size.

**A single value write is a transaction of one.** A caller writing one
attribute per request — the usual REST flow — cannot set the condition first
and the required value second. Either write both in one `POST /values/batch`,
or use `on_read` and gate at a workflow boundary. This is the real cost of
`on_write`, and it is why it is not the default.

**Removing a value is checked too.** Taking `allergens` away from a dish that
declares it has them leaves exactly the state the write was refused for, so it
is refused on the same terms. Deleting the whole entity is allowed: an entity
with nothing left is gone, not incomplete.

### Adding a blocking rule to data that already exists

Adding an `on_write` rule does **not** re-validate what is already stored.
Nothing is rewritten, nothing is archived, and no read starts failing. Entities
that already sit in the state the rule forbids stay exactly as they are, and
completeness reports them the way it always did.

The rule applies from the next write to each entity. A caller that touches such
an entity — for any attribute — is refused until the gap is filled, so an
existing violation surfaces when someone next edits that record rather than in
a sweep.

Two consequences worth planning for:

- Check the population **before** switching a rule to `on_write`. Type
  completeness (`GET /type-definitions/{id}/completeness`) tells you how many
  entities the rule will start blocking.
- The path that must never be blocked is not. **Erasure and purge are not
  gated**: a tenant that cannot delete a record because a dependency says it is
  incomplete has a compliance problem the schema created.

### What is never gated on write

An attribute's **own** `required` flag. It describes the finished record, and
gating it would make an entity impossible to create — the first value written
always leaves the others empty. Only a requirement that came from a **matched
dependency** can refuse a write, because that one is conditional on values the
entity already holds.

Completeness reports both, and always has.

### Reading the mode

The effective schema says which kind of requirement a caller is looking at, so
a UI can tell a rule from a wall before it lets someone save half a record:

```bash
curl -s "localhost:8080/api/v1/entities/<dish-type-id>/tart/attributes/<allergens>/effective-schema"
# {"required":true,"required_enforcement":"on_write","restricted":false}
```

## Conflicting rules

Several dependencies can target one attribute, and they are resolved by rule
rather than by whichever the store returned last — the two backends do not
return them in the same order.

| Conflict | Resolution |
| --- | --- |
| One rule requires, another relaxes | **Required.** The permissive answer lets a record past a gate someone configured to stop it. |
| Two rules require, one `on_write` and one `on_read` | **`on_write`**, for the same reason. |
| Two rules narrow the allowed values | The **intersection**, which may be empty. |

A rule that does not match contributes nothing — including its mode. A rule
that *relaxes* a requirement has no enforcement to contribute either, so it
cannot turn into a block.

### What a restricted caller sees

A rule is resolved over the values the **caller may read**, exactly as
completeness and the effective schema are, and an attribute the caller cannot
read is skipped. Two consequences, both deliberate:

- A principal who cannot see a rule's source is not refused by it. Refusing
  would answer, with one bit per entity, a question about a value the field ACL
  hides everywhere else.
- That principal can therefore write a state it cannot see is forbidden. This
  is the same trade the read paths already make, and it is what keeps the gate,
  the effective schema and completeness giving one answer.

A caller with full read access gets the rule in full.

## Validation

- `enforce` must be `on_write` or `on_read`.
- `enforce` is refused on an effect that does not **require** a value —
  including one that sets `required: false`. A stored setting that changes
  nothing reads as a configured rule, and a relaxing rule carrying a mode used
  to drag an unrelated demanding rule into blocking.
- A **computed** attribute cannot be required on write. Its value is
  materialized after the write commits, so the demand could never be met while
  the write is judged; the rule is refused where it is authored.
- An effect must do something: narrow values, add constraints, or override
  required.

## Why reporting is the default

A schema here **describes** what an entity needs, completeness **reports** what
it is missing, and the application **decides** when the gap matters. Entities
are assembled over time by several hands — a supplier feed, a translator, a
merchandiser — and a store that refuses a half-assembled record has no use for
a completeness score.

Blocking is therefore the deliberate exception, declared per rule. A rule
stored without `enforce` behaves exactly as it did before the field existed and
is not rewritten with a mode.

## See also

- [`docs/computed.md`](computed.md) — derived values, which a dependency can
  read as a source.
- [`examples/kitchen/`](../examples/kitchen/) — an `on_read` rule whose gap is
  turned into a refusal at publish time, by the host, from the service's own
  completeness report.

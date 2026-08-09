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

### on_read: the rule describes, something else decides

This is the default, and it is what every dependency did before `enforce`
existed. Setting `contains_allergens` to `true` on a dish with no allergens is
accepted, and the dish is then reported incomplete:

```bash
curl -s localhost:8080/entities/dish/tart/completeness
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
curl -s -X POST localhost:8080/values -d '{"attribute_definition_id":"<contains_allergens>","entity_id":"tart","value":true}'
# 422 {"error":{"code":"dependency_violation",
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

In best-effort import mode each row is its own transaction, so one refused row
does not cost the others.

**A single value write is a transaction of one.** A caller writing one
attribute per request — the usual REST flow — cannot set the condition first
and the required value second. Either write both in one `POST /values/batch`,
or use `on_read` and gate at a workflow boundary. This is the real cost of
`on_write`, and it is why it is not the default.

**Removing a value is checked too.** Taking `allergens` away from a dish that
declares it has them leaves exactly the state the write was refused for, so it
is refused on the same terms. Deleting the whole entity is allowed: an entity
with nothing left is gone, not incomplete.

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
curl -s "localhost:8080/dependencies/effective-schema?attribute_definition_id=<allergens>&entity_id=tart"
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

## Validation

- `enforce` must be `on_write` or `on_read`.
- `enforce` is refused on an effect that does not set `required`. A stored
  setting that changes nothing reads as a configured rule, which is worse than
  an error.
- An effect must do something: narrow values, add constraints, or override
  required.

## Compatibility

A rule stored without `enforce` behaves exactly as it did before the field
existed, and is not rewritten with a mode. Blocking is opt-in per rule: the
default could not be `on_write` without making every stored rule start refusing
writes on upgrade.

## See also

- [`docs/computed.md`](computed.md) — derived values, which a dependency can
  read as a source.
- [`examples/kitchen/`](../examples/kitchen/) — an `on_read` rule whose gap is
  turned into a refusal at publish time, by the host, from the service's own
  completeness report.

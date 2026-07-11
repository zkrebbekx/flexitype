# Entity-type inheritance — Design

Soft types form hierarchies: `MountainBike extends Bike extends Product`.
A subtype inherits every attribute, constraint and dependency of its
ancestors; entities declared as a subtype validate against — and are
edited through — the full inherited schema. This document fixes the
semantics so backend and UI implement one model.

## Decisions

**`extends` is immutable.** Set at creation, never changed. Re-parenting a
type would silently re-shape the effective schema of every existing entity;
if a hierarchy was modelled wrongly, create a new type and migrate data
deliberately. This matches the existing immutability of `internal_name` and
`data_type`.

**Single inheritance, entity kinds only.** One parent per type, same
tenant, both `kind = entity` (relationship attribute-set types cannot
participate). Chains are cycle-checked and bounded (depth 10 — deeper
hierarchies are a modelling smell).

**No shadowing, hierarchy-wide.** An attribute's `internal_name` must be
unique across the type's whole hierarchy: creating an attribute is rejected
when the name is already declared by any ancestor *or any descendant*.
Without the descendant check, adding `weight` to `Product` after
`MountainBike` declared its own `weight` would make resolution ambiguous.
The write path locks the root of the hierarchy so concurrent creates on
different levels serialize.

**Values anchor to the entity's declared type.** An entity *is* one type
(the most-derived one). When `mtb-01` (a `MountainBike`) sets `price`
(declared on `Product`), the stored value carries
`type_definition_id = MountainBike` — hydration by (type, entity) stays one
indexed lookup and never misses inherited values. The write path validates
that the attribute's declaring type is an ancestor-or-self of the entity's
declared type. The Set API accepts the entity's type; omitted, it defaults
to the attribute's declaring type (the pre-inheritance behaviour).

**Uniqueness is hierarchy-wide.** A `unique` attribute declared on
`Product` admits one value across every entity of every descendant type —
the probe filters by `attribute_definition_id` alone, which is already the
declaring definition regardless of the entity's subtype.

**Dependencies may span levels.** Source and target attributes must belong
to one chain (one declaring type an ancestor-or-self of the other), so the
rule is guaranteed evaluable on any entity that holds the target. Effective
schema resolution feeds the entity's full value set (which includes
inherited attributes) into the existing resolver unchanged.

**Relationships are polymorphic on endpoints.** A relationship definition
whose parent side is `Product` accepts links from entities of any
descendant type; the console offers a relationship type on a subtype's page
when its endpoint matches any ancestor.

## Effective attributes

`GET /type-definitions/{id}/effective-attributes` returns the chain-resolved
attribute list, each entry tagged with the declaring type. Own attributes
first, then each ancestor's, root last. The entity inspector, dependency
builder and validation paths all consume this one shape, so the subtype
experience is identical to the plain-type experience everywhere.

## API surface

```
POST /type-definitions                       + extends_id
GET  /type-definitions/{id}/effective-attributes
GET  /type-definitions/{id}/children
GET  /entities/{typeId}?include_descendants=true    rows gain the entity's type
POST /values                                 + type_definition_id (entity's declared type)
```

Type snapshots expose `extends_id`; the console builds the hierarchy tree
client-side from the flat list.

## Console

- **Types page** renders the hierarchy as an indented tree (children under
  parents, connector lines, subtype counts) instead of a flat table.
- **Create drawer** gains an "Extends" select with plain-language help.
- **Type detail** header shows the ancestor chain as linked chips and a
  "Subtypes" row; the attributes tab splits into *Declared here*
  (editable) and *Inherited* (read-only, "from Bike" badge linking to the
  declaring type).
- **Entity inspector** renders the effective attribute set, so inherited
  attributes are editable in place; value writes carry the entity's type.
- **Entity browsing** gets an "include subtypes" toggle; descendant rows
  carry a type badge.
- **Dependency builder** offers effective attributes, enabling cross-level
  rules.

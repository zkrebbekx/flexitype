# FQL — the flexitype query language

FQL queries entities by their attribute values and across the
relationships you've defined. It is schema-aware: attribute names resolve
against the queried type's *effective* (inherited) schema, literals are
coerced by the attribute's data type, and operators are validated against
what the type supports — errors carry positions so editors can underline
them.

## Shape

A query runs against one root type (subtypes included by default) and is a
boolean expression over conditions:

```
category = "bike" and (min(price) >= 500 or tags in ("sale")) and
child(supplied_by) { contains(contact_email, "acme") and link.lead_time_days <= 14 }
```

## Grammar

```
expr       := or
or         := and ( OR and )*
and        := unary ( AND unary )*
unary      := NOT unary | primary
primary    := "(" expr ")" | traversal | condition
traversal  := ("child" | "parent" | "linked") "(" ident ")" "{" expr "}"
condition  := comparison | inExpr | boolFunc
comparison := operand op literal
inExpr     := operand IN "(" literal ("," literal)* ")"
boolFunc   := RANGE "(" operand "," literal "," literal ")"
            | HAS "(" field ")"
            | CONTAINS  "(" field "," literal ")"
            | ICONTAINS "(" field "," literal ")"
            | IEQUALS   "(" field "," literal ")"
            | MATCHES   "(" literal ")"
operand    := (MIN | MAX | COUNT | LENGTH) "(" field ")" | field
field      := ident | "link" "." ident | "type"
op         := "=" | "!=" | ">" | ">=" | "<" | "<=" | "isa"
            | EQ | NEQ | GT | GTE | LT | LTE
literal    := string | number | true | false | ident
quantity   := number ident        // a bare unit suffix: `weight > 1500 g`
```

Keywords are case-insensitive; identifiers are attribute / relationship /
type internal names. Strings take double or single quotes.

Three constructs above are easy to miss, and two of them are mandatory
rather than optional:

- **`linked(rel) { ... }`** traverses a relationship without naming a
  direction. A **symmetric** relationship has no parent or child, so
  `child()`/`parent()` are refused for one and `linked()` is the only way to
  traverse it.
- **A unit suffix is required on a quantity comparison.** `weight > 1500`
  is refused; write `weight > 1500 g`. Without the unit the bound has no
  meaning, because values are stored against a family's base unit.
- **`matches("...")`** is best-effort relevance search. It is deliberately
  excluded from the backend-parity corpus (see the note below), so prefer
  `contains`/`iequals` when you need a deterministic predicate.

The left side of `in` is the attribute, not the literal: write
`tags in ("sale")`, not `"sale" in tags`.

## Semantics

- **Multi-valued attributes** match if *any* value satisfies the condition
  (wrap in `not(...)` for none). `min` / `max` / `count` aggregate across
  the entity's values; `count` is 0 for absent attributes.
- **`length(field)`** is the character count of a textual value.
- **`range(x, lo, hi)`** is inclusive on both ends.
- **`has(field)`** — the entity holds at least one live value.
- **`contains` / `icontains` / `iequals`** — substring and equality on
  textual attributes; the `i` forms are case-insensitive.
- **`type`** is a virtual field: `type = ebike` matches the exact type,
  `type isa product` matches the type or any descendant, `type in (a, b)`
  enumerates.
- **Traversals** follow a relationship definition from the current entity:
  `child(rel) { ... }` requires a live link where the current entity is the
  parent and the child-side counterpart satisfies the inner expression;
  `parent(rel)` is the mirror. Inside the braces, plain fields resolve
  against the counterpart's schema and `link.x` resolves against the
  relationship's own (inherited) attribute set. Traversals nest.
- **Inheritance** applies everywhere: field names resolve across the root
  type's ancestors and descendants (shadowing rules make this unambiguous),
  and relationship endpoints are polymorphic.

## Execution

The parser (pkg/fql, dependency-free) produces a positioned AST. The
binder (application/query) resolves names to attribute / relationship /
type definitions, coerces literals via the attribute's data type and
rejects unsupported operator/type pairs. The compiler
(infrastructure/postgres) turns the bound tree into one SQL statement:
each condition becomes an `EXISTS` (or aggregate) subquery against the
value table, traversals become correlated subqueries through the
relationship table, and the whole tree composes with `AND`/`OR`/`NOT`.
Identifiers never reach SQL — only resolved ULIDs and bound `?` arguments
do.

```
GET  /api/v1/query?type=<internal_name>&q=<expr>&limit=&cursor=&total=&locale=&channel=
POST /api/v1/query/validate   {type, q} → {"valid": true} or a positioned error
```

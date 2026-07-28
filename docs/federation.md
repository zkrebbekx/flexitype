# GraphQL federation

flexitype can serve its GraphQL endpoint as an **Apollo Federation subgraph**,
so a gateway composes it into a federated graph together with the services that
own your entities.

This is the natural shape for an attribute service. Your entity ids belong to
another system; flexitype holds the dynamic attributes for them. With
federation, a client asks the gateway for a `Product`, the product subgraph
answers the fields it owns, and flexitype answers the attribute fields — one
query, no stitching in your own code.

Without it, the endpoint is a standalone schema. A gateway cannot compose it at
all, so the only way to reach the attributes was to call the REST API from your
own graph layer and merge the results by hand.

## Enable it

| Deployment | How |
| --- | --- |
| Standalone server | `FLEXITYPE_FEATURE_GRAPHQL_FEDERATION=true` |
| Embedded library | `flexitype.New(pool, flexitype.WithGraphQLFederation())` |

It is off by default. A federated schema carries three fields no standalone
client asks for, and `_entities` is a batch read a non-federated deployment has
no reason to expose.

## What the subgraph exposes

| Field | Purpose |
| --- | --- |
| `_service { sdl }` | The subgraph's schema as SDL, which the gateway composes against. |
| `_entities(representations: [_Any!]!)` | Resolves entities from representations another subgraph supplies. |
| `@key(fields: "entityId")` | On every entity type, in the SDL. |

The key is `entityId` — the same opaque id every other surface uses, and the id
your own system already owns. The gateway needs nothing else to join.

## Example

Read the SDL the way a gateway does:

```graphql
{ _service { sdl } }
```

```graphql
type Product @key(fields: "entityId") {
  entityId: String!
  name: String
  stock: Int
  tags: [String]
  suppliers(first: Int, after: String): SupplierConnection
}
```

Resolve two entities the gateway already knows by id:

```graphql
query($r: [_Any!]!) {
  _entities(representations: $r) {
    ... on Product { entityId name stock }
  }
}
```

```json
{ "r": [
  { "__typename": "Product", "entityId": "sku-1" },
  { "__typename": "Product", "entityId": "sku-2" }
] }
```

In your gateway's supergraph, declare the entity in the subgraph that owns it
and let flexitype contribute the attribute fields:

```graphql
# the product subgraph
type Product @key(fields: "entityId") {
  entityId: String!
  price: Money!
}
```

## Rules worth knowing

- **The answer preserves order.** `_entities` returns one element per
  representation, at the same index. Position is the only thing that ties a
  result to its representation.
- **An id with no values comes back with null fields, not an error.**
  flexitype does not own entity existence — the entity id is supplied by the
  caller and owned by another system, which is the case federation exists for.
  "No values here" is the honest answer; "no such entity" would not be.
- **A representation naming a type this subgraph does not have is refused**,
  and so is one missing `entityId`. Both are gateway-side mistakes, and both
  answer with a message that names what was wrong.
- **A batch is capped at 500 representations.** The batch is a single list
  argument, so it escapes the field-count cost guard: one small document could
  otherwise ask for tens of thousands of entities. A gateway that needs more
  pages the join itself.
- **The field ACL applies.** The SDL a caller reads carries only the types and
  attributes that caller may read, and `_entities` resolves the same set. An
  attribute a caller cannot read is not in its schema and cannot be asked for.
- **The schema is per tenant.** Each tenant composes its own SDL from its own
  type definitions, and the SDL changes when the definitions change. A gateway
  that pins a supergraph must recompose after a schema change, exactly as it
  does for any other subgraph.
- **The endpoint stays read-only.** Federation adds no write path.
- **`_entities` is omitted for a tenant with no readable types.** GraphQL
  forbids an empty union, so there is nothing for a gateway to resolve;
  `_service` still answers.

## The SDL is generated, not introspected

The federation directives are not part of the executable schema — introspection
would not report `@key` — so `_service { sdl }` returns a rendering built from
the same type metadata the executable schema is built from. The rendering omits
the federation additions (`_entities`, `_service`, `_Any`, `_Entity`), as the
specification requires.

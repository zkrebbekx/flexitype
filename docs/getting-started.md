# Getting started

A ten-minute path from nothing to a validated, queryable value with a
cascading dependency. It uses the standalone service in development mode (no
auth). To try it with zero setup instead, open the
[playground](https://zkrebbekx.github.io/flexitype/) — the same API runs
entirely in your browser.

## 1. Run the service

```bash
docker run --rm -p 8080:8080 ghcr.io/zkrebbekx/flexitype:latest
# or, from a checkout: go run ./cmd/flexitype
```

With no service accounts configured the API runs unauthenticated (development
mode), so the calls below need no token. Check it is up:

```bash
curl -s localhost:8080/readyz
```

All calls below hit `http://localhost:8080/api/v1`.

## 2. Define a type

```bash
curl -s localhost:8080/api/v1/type-definitions \
  -d '{"internal_name":"product","display_name":"Product"}'
```

Note the returned `id` (call it `$PRODUCT`).

## 3. Add an attribute with a constraint

A `price` that must be non-negative:

```bash
curl -s localhost:8080/api/v1/attributes \
  -d '{"type_definition_id":"'"$PRODUCT"'","internal_name":"price",
       "display_name":"Price","data_type":"integer",
       "constraints":[{"kind":"min_value","value":{"type":"integer","value":0}}]}'
```

Each attribute create returns an `id`; capture the ones you'll reuse below as
`$PRICE`, `$MATERIAL` and `$GRADE`.

Add a `material` string too — the source of the dependency in step 6:

```bash
curl -s localhost:8080/api/v1/attributes \
  -d '{"type_definition_id":"'"$PRODUCT"'","internal_name":"material",
       "display_name":"Material","data_type":"string"}'

curl -s localhost:8080/api/v1/attributes \
  -d '{"type_definition_id":"'"$PRODUCT"'","internal_name":"grade",
       "display_name":"Grade","data_type":"string"}'
```

## 4. Set a value

Values hang off an opaque `entity_id` you choose (a SKU, a UUID, anything):

```bash
curl -s localhost:8080/api/v1/values \
  -d '{"attribute_definition_id":"'"$PRICE"'","entity_id":"sku-1",
       "type_definition_id":"'"$PRODUCT"'","value":150}'
```

A value that violates a constraint (e.g. `-1`) comes back `422 VALIDATION`.

## 5. Query with FQL

```bash
curl -s "localhost:8080/api/v1/query?type=product&q=price%20%3E%20100"
# → entities whose price > 100, including sku-1
```

FQL understands comparisons, `in`, `range`, `has`, string matching,
`type isa`, relationship traversals and more — see the README.

## 6. Add a cascading dependency

Make the allowed `grade` values depend on `material` — a conditional picklist:

```bash
curl -s localhost:8080/api/v1/dependencies \
  -d '{"type_definition_id":"'"$PRODUCT"'",
       "source_attribute_id":"'"$MATERIAL"'",
       "target_attribute_id":"'"$GRADE"'",
       "conditions":[{"operator":"equals","value":{"type":"string","value":"steel"}}],
       "effect":{"allowed_values":[{"type":"string","value":"304"},
                                   {"type":"string","value":"316"}]}}'
```

Now, for an entity whose `material` is `steel`, setting `grade` to anything
other than `304`/`316` is rejected; change `material` and the allowed grades
change with it. Read the effective schema for an entity's attribute to see the
resolved allowed set:

```bash
curl -s "localhost:8080/api/v1/entities/$PRODUCT/sku-1/attributes/$GRADE/effective-schema"
```

## Where next

- [Configuration](configuration.md) — auth, provisioning, feature flags.
- [API clients](clients.md) — the first-party Go client and OpenAPI generation.
- The [README](../README.md) — the full feature tour (relationships, units,
  media, revisions, change-sets, GraphQL, events).

# @flexitype/client

A TypeScript client for the flexitype REST API, with a second entry point for
React hooks.

**This package is not published to npm.** It lives in this repository and is
versioned alongside the API it speaks to, so the client that ships with a
release is the client that matches that release. Consume it from a checkout, a
workspace, or a git dependency.

- `@flexitype/client` — the core. It is framework-agnostic and imports no
  React, so the Vue console can adopt it.
- `@flexitype/client/react` — hooks over TanStack Query. `react` and
  `@tanstack/react-query` are peer dependencies.

The core has **no runtime dependencies**. It uses `fetch`, which Node 20 and
every supported browser provide. Both entry points ship ESM and CJS with type
declarations.

## The types are generated, not written

Every data shape comes from `api/openapi.yaml` through `openapi-typescript`:

```bash
npm run generate   # rewrites src/generated/openapi.ts
```

The generated file is checked in, and `test/generated.test.ts` regenerates it
and fails when the checked-in copy is stale. So the client cannot describe a
shape the API document does not.

A field keeps its wire name (`internal_name`, `page_info`, `attribute_definition_id`).
A rename layer would have to be maintained by hand, which is the drift this
arrangement exists to prevent. The options objects the SDK invents — pagination
and filters — are camelCase, because no document defines them.

## One client is one tenant

**The tenant travels in the token.** The service reads it from the
authenticated service account (`internal/interfaces/http/middleware.go` sets
the request tenant from `account.Tenant()`), never from a header or a
parameter. So a client instance talks to exactly one tenant, and there is no
way to make it talk to another.

An application that serves several tenants builds one client per tenant:

```ts
import { createClient, type FlexitypeClient } from '@flexitype/client'

const clients = new Map<string, FlexitypeClient>()

function clientFor(tenantToken: string): FlexitypeClient {
  const existing = clients.get(tenantToken)
  if (existing !== undefined) return existing
  const created = createClient({ baseUrl: 'https://flexitype.internal', token: tenantToken })
  clients.set(tenantToken, created)
  return created
}
```

## Creating a client

```ts
import { createClient, defaultRetryPolicy } from '@flexitype/client'

const client = createClient({
  baseUrl: 'https://flexitype.internal', // "/api/v1" is added when absent
  token: process.env.FLEXITYPE_TOKEN,
  // Optional:
  // fetch: myTracingFetch,
  // userAgent: 'storefront/1.0',   // a browser refuses to set this; omit it there
  // headers: { 'X-Request-Source': 'storefront' },
  retry: defaultRetryPolicy(),
})
```

A browser app served by the service itself passes a path:

```ts
const sameOrigin = createClient({ baseUrl: '/api/v1', token: sessionToken })
```

### Retrying

Retrying is on by default and applies to an **idempotent read only**: GET,
HEAD, PUT and DELETE. A POST is never replayed, whatever the policy says,
because a POST that wrote a value may have been applied before the connection
broke. The policy mirrors `client/retry.go`: three attempts on 429 and the
transient 5xx statuses, backing off from 200 ms to 5 s with jitter, honouring a
`Retry-After` header over its own backoff.

422 is deliberately not retryable. A cursor of the wrong arity, or one the
ordering column cannot parse, answers 422 by design; replaying it repeats the
same answer.

Pass `retry: false` to switch it off.

## Errors

Every failure is a `FlexitypeError` carrying the API's stable machine code, the
HTTP status, the message and the details:

```ts
import { isNotFound, isFlexitypeError, isRateLimited } from '@flexitype/client'

try {
  await client.types.get(typeId)
} catch (error) {
  if (isNotFound(error)) return null
  if (isRateLimited(error) && isFlexitypeError(error)) {
    console.warn(`throttled; retry in ${error.retryAfterMs ?? 0}ms`)
  }
  throw error
}
```

The codes are `VALIDATION`, `NOT_FOUND`, `CONFLICT`, `ARCHIVED`,
`DEPENDENCY_VIOLATION`, `FEATURE_DISABLED`, `CURSOR_CONFLICT`,
`CURSOR_EXPIRED`, `UNAUTHENTICATED`, `FORBIDDEN`, `RATE_LIMITED` and
`INTERNAL`. `test/error-codes.test.ts` holds this list equal to the OpenAPI
enum, the Go client's constants and `docs/api-stability.md`.

A request that never reached the service carries `status: 0` and the code
`INTERNAL`, with the transport failure in `cause`.

## Listing and paging

The API pages by an opaque keyset cursor. Every list call returns one page; a
matching `listAll` walks them.

```ts
// One page.
const page = await client.entities.list(typeId, { limit: 50, total: true })
console.log(page.items.length, page.page_info.total_count, page.page_info.next_cursor)

// Every page, as an async iterator.
for await (const entity of client.entities.listAll(typeId)) {
  console.log(entity.entity_id)
}

// An FQL query, the same two ways.
const matches = await client.query.run('product', 'price > 10', { limit: 25 })
console.log(matches.items.length)
for await (const row of client.query.runAll('product', 'price > 10')) {
  console.log(row.entity_id)
}
```

`page_info.has_previous_page` says a previous page exists, but the API has no
backward cursor. `CursorStack` keeps the cursors a screen has visited so a Back
button can work:

```ts
import { CursorStack } from '@flexitype/client'

const stack = new CursorStack()
let current = await client.types.list({ cursor: stack.current() })

async function next(): Promise<void> {
  if (stack.push(current.page_info)) current = await client.types.list({ cursor: stack.current() })
}

async function back(): Promise<void> {
  if (stack.pop()) current = await client.types.list({ cursor: stack.current() })
}
```

## Rendering a form from a soft schema

flexitype types are defined at runtime, so no generated interface describes an
entity's fields. Build the form from the type's **effective attributes** — its
own plus everything it inherits through `extends_id`:

```ts
import { loadFormDescriptor, toWire, type FormField } from '@flexitype/client'

const form = await loadFormDescriptor(client, typeId)

for (const group of form.groups) {
  console.log(group.name ?? 'General')
  for (const field of group.fields) renderField(field)
}

function renderField(field: FormField): void {
  // field.kind is one of text, number, decimal, checkbox, select, date, time,
  // datetime, json, file, quantity, url, email.
  console.log(field.kind, field.label, field.required, field.options, field.constraints)
}
```

Write a value back through `toWire`, which encodes an application value into
the form the API expects for that data type:

```ts
const field = form.byName.price
if (field !== undefined) {
  await client.values.set({
    attribute_definition_id: field.attributeId,
    entity_id: 'p-1001',
    type_definition_id: typeId,
    value: toWire(field.dataType, '19.99'),
  })
}
```

### Decimals and quantities

A decimal is a **string** end to end. `toWire('decimal', 19.99)` is refused
rather than rounded, because a JavaScript number holds 15 to 17 significant
digits and a price that passes through one can come back changed. Compare and
format decimals as text:

```ts
import { compareValues, formatDecimal } from '@flexitype/client'

const prices = ['9', '10', '1.5']
prices.sort((a, b) => compareValues('decimal', a, b)) // ['1.5', '9', '10']
formatDecimal('123456789012345678901', 'en-US') // '123,456,789,012,345,678,901'
```

A quantity carries a magnitude, a unit and a base magnitude. The service
resolves the unit against the attribute's unit family and computes the base, so
a write sends only the magnitude and the unit. Values compare on the base, so
1500 g sorts above 1 kg:

```ts
import { compareQuantities, convertQuantity, withBase } from '@flexitype/client'

compareQuantities({ magnitude: '1', unit: 'kg', base: 1000 }, { magnitude: '1500', unit: 'g', base: 1500 }) // < 0

const [family] = await client.unitFamilies.list()
if (family !== undefined) {
  withBase({ magnitude: '2', unit: 'kg' }, family)          // { magnitude: '2', unit: 'kg', base: 2000 }
  convertQuantity({ magnitude: '2', unit: 'kg' }, 'g', family)
}
```

### Scoped values

An attribute may be localizable, scopable, both or neither, so a value is
addressed by (attribute, locale, channel). `GET /entities/{type}/{id}/values`
returns one row per scope; keying a screen by attribute id alone shows one
locale's value under another's label.

```ts
import { ScopedValues, scopedValueInput } from '@flexitype/client'

const values = new ScopedValues(await client.entities.values(typeId, 'p-1001'))

values.get(nameAttributeId)                                          // the base value
values.get(nameAttributeId, { locale: 'fr' })                        // the French one, or undefined
values.get(nameAttributeId, { locale: 'de' }, { fallbackToBase: true })
values.locales()                                                     // ['fr']

await client.values.set(
  scopedValueInput({
    attributeDefinitionId: nameAttributeId,
    entityId: 'p-1001',
    typeDefinitionId: typeId,
    value: 'Bidule',
    scope: { locale: 'fr' },
  }),
)
```

### Dependency-resolved rules

A dependency can make an attribute required that the definition does not, and
can narrow the values it allows. Read the resolved state for one entity and
merge it into the descriptor:

```ts
import { toFormDescriptor } from '@flexitype/client'

const attributes = await client.types.effectiveAttributes(typeId)
const colour = await client.entities.effectiveSchema(typeId, 'p-1001', colourAttributeId)

const resolved = toFormDescriptor(attributes, { overrides: { [colourAttributeId]: colour } })
console.log(resolved.byName.colour?.required) // what this entity's rules say, not the static schema
console.log(resolved.byName.colour?.options)  // only the values the dependency still allows
```

## React

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createClient } from '@flexitype/client'
import { FlexitypeProvider, useFormDescriptor, useInfiniteQueryEntities } from '@flexitype/client/react'

const queryClient = new QueryClient()
const flexitype = createClient({ baseUrl: '/api/v1', token })

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <FlexitypeProvider client={flexitype}>
        <ProductList />
      </FlexitypeProvider>
    </QueryClientProvider>
  )
}
```

`FlexitypeProvider` deliberately does not create a `QueryClientProvider`: the
cache belongs to the application, and a second one would split it in two so an
invalidation from application code missed these hooks.

### The three states

Every list hook exposes `state`, one of `pending`, `error`, `empty` or `ready`.
The console shipped a bug (#496) where a failed request rendered as "no
results"; a screen that switches on `state` cannot repeat it.

```tsx
import { useQueryEntities } from '@flexitype/client/react'

function ProductList() {
  const products = useQueryEntities('product', 'price > 10')
  switch (products.state) {
    case 'pending':
      return <Spinner />
    case 'error':
      return <ErrorPanel code={products.error?.code} message={products.error?.message} />
    case 'empty':
      return <p>No product costs more than 10.</p>
    default:
      return <ul>{products.data?.items.map((p) => <li key={p.entity_id}>{p.entity_id}</li>)}</ul>
  }
}
```

### Infinite paging

```tsx
import { flattenPages, useInfiniteQueryEntities } from '@flexitype/client/react'

function ProductFeed() {
  const feed = useInfiniteQueryEntities('product', 'status = "active"', { limit: 50 })
  const rows = flattenPages(feed.data)
  return (
    <>
      {rows.map((row) => <Row key={row.entity_id} id={row.entity_id} />)}
      {feed.hasNextPage === true && <button onClick={() => void feed.fetchNextPage()}>More</button>}
    </>
  )
}
```

### A dynamic form

```tsx
import { toWire } from '@flexitype/client'
import { useEntityValues, useFormDescriptor, useSetValue } from '@flexitype/client/react'

function EntityForm({ typeId, entityId }: { typeId: string; entityId: string }) {
  const schema = useFormDescriptor(typeId)
  const values = useEntityValues(typeId, entityId)
  const write = useSetValue()

  if (schema.state === 'pending' || values.state === 'pending') return <Spinner />
  if (schema.state === 'error') return <ErrorPanel message={schema.error?.message} />

  return (
    <form>
      {schema.descriptor?.fields.map((field) => (
        <Field
          key={field.attributeId}
          field={field}
          value={values.scoped?.valueOf(field.attributeId)}
          onChange={(next: unknown) =>
            write.mutate({
              attribute_definition_id: field.attributeId,
              entity_id: entityId,
              type_definition_id: typeId,
              value: toWire(field.dataType, next),
            })
          }
        />
      ))}
    </form>
  )
}
```

### Query keys

Every key starts with `'flexitype'`, then names a resource, then a shape, then
the arguments — widest first, narrowest last, because TanStack Query matches by
prefix. A derived query hangs off the key of the record it derives from:

| key | covers |
|---|---|
| `['flexitype']` | everything this package caches |
| `['flexitype','types']` | every type query |
| `['flexitype','types','list',opts]` | one page of types |
| `['flexitype','types','detail',id]` | one type and everything derived from it |
| `['flexitype','types','detail',id,'effective-attributes']` | that type's effective attributes |
| `['flexitype','entities','list',typeId,opts]` | one page of a type's entities |
| `['flexitype','entities','detail',typeId,entityId]` | one entity and all its derived queries |
| `['flexitype','entities','detail',typeId,entityId,'values',opts]` | that entity's values |
| `['flexitype','query','run',type,q,opts]` | one FQL result set |

Build a key with `flexitypeKeys` rather than by hand:

```ts
import { flexitypeKeys } from '@flexitype/client/react'

// One entity's values, completeness, relationships and revisions, in one call.
await queryClient.invalidateQueries({ queryKey: flexitypeKeys.entities.detail(typeId, entityId) })

// Every type query, whatever its arguments.
await queryClient.invalidateQueries({ queryKey: flexitypeKeys.types.all })
```

The scheme is stable API: a key's prefix does not change in a minor release.

### Retrying in the hooks

TanStack's own retry is **off** in these hooks. The transport already retries
an idempotent read and honours the service's `Retry-After`; leaving both on
would multiply them into up to nine requests for one render. Pass `retry` in a
hook's options to turn it back on.

## Development

```bash
npm install
npm run generate    # regenerate the API types from ../api/openapi.yaml
npm run lint
npx tsc --noEmit
npm test
npm run build
```

CI runs all of these on every pull request, in the `client-ts` job of
`.github/workflows/ci.yml`.

The tests use a fetch mock, never a live service. They cover the error code for
every documented failure, the retry policy, the pagination iterator across a
page boundary, value coercion for every data type, effective-attribute
resolution through inheritance, and the React hooks.

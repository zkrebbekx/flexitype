/**
 * Every code sample in README.md, compiled.
 *
 * A README whose snippets do not compile is worse than none: a reader trusts
 * it, writes what it shows, and finds out at run time. This file holds each
 * sample verbatim, imports the package by its published name, and is covered
 * by `npx tsc --noEmit`, which CI runs. A sample that goes stale fails the
 * build.
 *
 * It is not part of the published output — tsup builds `src` only.
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import {
  compareQuantities,
  compareValues,
  convertQuantity,
  createClient,
  CursorStack,
  defaultRetryPolicy,
  formatDecimal,
  isFlexitypeError,
  isNotFound,
  isRateLimited,
  loadFormDescriptor,
  ScopedValues,
  scopedValueInput,
  toFormDescriptor,
  toWire,
  withBase,
  type FlexitypeClient,
  type FormField,
} from '@flexitype/client'
import {
  flattenPages,
  FlexitypeProvider,
  flexitypeKeys,
  useEntityValues,
  useFormDescriptor,
  useInfiniteQueryEntities,
  useQueryEntities,
  useSetValue,
} from '@flexitype/client/react'

// The query keys come from the React entry point only: the core stays free of
// anything TanStack Query defines.

const token = 'ft_account_secret'
const typeId = 'T-1'
const nameAttributeId = 'A-name'
const colourAttributeId = 'A-colour'
const sessionToken = token

// --- One client is one tenant -----------------------------------------------

const clients = new Map<string, FlexitypeClient>()

export function clientFor(tenantToken: string): FlexitypeClient {
  const existing = clients.get(tenantToken)
  if (existing !== undefined) return existing
  const created = createClient({ baseUrl: 'https://flexitype.internal', token: tenantToken })
  clients.set(tenantToken, created)
  return created
}

// --- Creating a client ------------------------------------------------------

const client = createClient({
  baseUrl: 'https://flexitype.internal', // "/api/v1" is added when absent
  token: process.env.FLEXITYPE_TOKEN,
  // Optional:
  // fetch: myTracingFetch,
  // userAgent: 'storefront/1.0',   // a browser refuses to set this; omit it there
  // headers: { 'X-Request-Source': 'storefront' },
  retry: defaultRetryPolicy(),
})

export const sameOrigin = createClient({ baseUrl: '/api/v1', token: sessionToken })

// --- Errors -----------------------------------------------------------------

export async function readType(): Promise<unknown> {
  try {
    return await client.types.get(typeId)
  } catch (error) {
    if (isNotFound(error)) return null
    if (isRateLimited(error) && isFlexitypeError(error)) {
      console.warn(`throttled; retry in ${error.retryAfterMs ?? 0}ms`)
    }
    throw error
  }
}

// --- Listing and paging -----------------------------------------------------

export async function listing(): Promise<void> {
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
}

export async function paging(): Promise<void> {
  const stack = new CursorStack()
  let current = await client.types.list({ cursor: stack.current() })

  async function next(): Promise<void> {
    if (stack.push(current.page_info)) current = await client.types.list({ cursor: stack.current() })
  }

  async function back(): Promise<void> {
    if (stack.pop()) current = await client.types.list({ cursor: stack.current() })
  }

  await next()
  await back()
}

// --- Rendering a form from a soft schema ------------------------------------

export async function dynamicForm(): Promise<void> {
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

  const field = form.byName.price
  if (field !== undefined) {
    await client.values.set({
      attribute_definition_id: field.attributeId,
      entity_id: 'p-1001',
      type_definition_id: typeId,
      value: toWire(field.dataType, '19.99'),
    })
  }
}

// --- Decimals and quantities ------------------------------------------------

export async function numbers(): Promise<void> {
  const prices = ['9', '10', '1.5']
  prices.sort((a, b) => compareValues('decimal', a, b)) // ['1.5', '9', '10']
  formatDecimal('123456789012345678901', 'en-US') // '123,456,789,012,345,678,901'

  compareQuantities({ magnitude: '1', unit: 'kg', base: 1000 }, { magnitude: '1500', unit: 'g', base: 1500 }) // < 0

  const [family] = await client.unitFamilies.list()
  if (family !== undefined) {
    withBase({ magnitude: '2', unit: 'kg' }, family) // { magnitude: '2', unit: 'kg', base: 2000 }
    convertQuantity({ magnitude: '2', unit: 'kg' }, 'g', family)
  }
}

// --- Scoped values ----------------------------------------------------------

export async function scoped(): Promise<void> {
  const values = new ScopedValues(await client.entities.values(typeId, 'p-1001'))

  values.get(nameAttributeId) // the base value
  values.get(nameAttributeId, { locale: 'fr' }) // the French one, or undefined
  values.get(nameAttributeId, { locale: 'de' }, { fallbackToBase: true })
  values.locales() // ['fr']

  await client.values.set(
    scopedValueInput({
      attributeDefinitionId: nameAttributeId,
      entityId: 'p-1001',
      typeDefinitionId: typeId,
      value: 'Bidule',
      scope: { locale: 'fr' },
    }),
  )
}

// --- Dependency-resolved rules ----------------------------------------------

export async function dependencyRules(): Promise<void> {
  const attributes = await client.types.effectiveAttributes(typeId)
  const colour = await client.entities.effectiveSchema(typeId, 'p-1001', colourAttributeId)

  const resolved = toFormDescriptor(attributes, { overrides: { [colourAttributeId]: colour } })
  console.log(resolved.byName.colour?.required) // what this entity's rules say, not the static schema
  console.log(resolved.byName.colour?.options) // only the values the dependency still allows
}

// --- React ------------------------------------------------------------------

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

export function ProductFeed() {
  const feed = useInfiniteQueryEntities('product', 'status = "active"', { limit: 50 })
  const rows = flattenPages(feed.data)
  return (
    <>
      {rows.map((row) => (
        <Row key={row.entity_id} id={row.entity_id} />
      ))}
      {feed.hasNextPage === true && <button onClick={() => void feed.fetchNextPage()}>More</button>}
    </>
  )
}

export function EntityForm({ typeId, entityId }: { typeId: string; entityId: string }) {
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

// --- Query keys -------------------------------------------------------------

export async function invalidation(entityId: string): Promise<void> {
  // One entity's values, completeness, relationships and revisions, in one call.
  await queryClient.invalidateQueries({ queryKey: flexitypeKeys.entities.detail(typeId, entityId) })

  // Every type query, whatever its arguments.
  await queryClient.invalidateQueries({ queryKey: flexitypeKeys.types.all })
}

// --- The stand-ins the samples render ---------------------------------------

function Spinner(): ReactNode {
  return <span>Loading…</span>
}

function ErrorPanel({ code, message }: { code?: string; message?: string }): ReactNode {
  return (
    <p role="alert">
      {code}: {message}
    </p>
  )
}

function Row({ id }: { id?: string }): ReactNode {
  return <div>{id}</div>
}

function Field(props: { field: FormField; value: unknown; onChange: (next: unknown) => void }): ReactNode {
  return (
    <label>
      {props.field.label}
      <input value={String(props.value ?? '')} onChange={(e) => props.onChange(e.target.value)} />
    </label>
  )
}

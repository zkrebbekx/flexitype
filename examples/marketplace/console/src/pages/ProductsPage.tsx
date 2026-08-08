import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueryEntities, useTypes } from '@flexitype/client/react'

import { Alert, Button, Card, SecondaryButton, Spinner, TextInput } from '../components/ui.js'

/**
 * The merchant's products, listed with FQL.
 *
 * `useQueryEntities` runs the query against the merchant's own tenant through
 * the passthrough. Rooting it at `product` also returns every SUBTYPE, so one
 * list shows a merchant's whole catalog whatever it modelled.
 */
export default function ProductsPage() {
  const { merchantId = '' } = useParams()
  const [type, setType] = useState('product')
  const [query, setQuery] = useState('has(name)')
  const [applied, setApplied] = useState({ type: 'product', query: 'has(name)' })

  const types = useTypes({ limit: 100 })
  const results = useQueryEntities(applied.type, applied.query, { limit: 50 })

  return (
    <div className="space-y-6">
      <Card title="Find products">
        <form
          className="grid gap-3 sm:grid-cols-[200px_1fr_auto] sm:items-end"
          onSubmit={(event) => {
            event.preventDefault()
            setApplied({ type, query: query.trim() === '' ? 'has(name)' : query.trim() })
          }}
        >
          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">Type</span>
            <select
              value={type}
              onChange={(event) => setType(event.target.value)}
              className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
            >
              {(types.data?.items ?? []).map((entry) => (
                <option key={entry.id} value={entry.internal_name}>
                  {entry.display_name}
                </option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">FQL</span>
            <TextInput
              value={query}
              spellCheck={false}
              placeholder='status = "active" and price < 100'
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
          <Button type="submit">Run</Button>
        </form>
      </Card>

      <Card title="Results">
        <div className="mb-4">
          <Link to={`/m/${merchantId}/products/new`}>
            <SecondaryButton type="button">New product</SecondaryButton>
          </Link>
        </div>

        {results.isPending && <Spinner label="Running the query" />}
        {results.isError && <Alert>{results.error.message}</Alert>}
        {results.isEmpty && !results.isPending && (
          <p className="text-sm text-slate-500">Nothing matched.</p>
        )}

        <ul className="divide-y divide-slate-100">
          {(results.data?.items ?? []).map((entity) => (
            <li key={entity.entity_id} className="flex items-center justify-between py-2">
              <Link
                to={`/m/${merchantId}/products/${encodeURIComponent(entity.entity_id ?? '')}?type=${applied.type}`}
                className="text-sm font-medium hover:underline"
              >
                {entity.entity_id}
              </Link>
              <span className="text-xs text-slate-500">{entity.value_count} values</span>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  )
}

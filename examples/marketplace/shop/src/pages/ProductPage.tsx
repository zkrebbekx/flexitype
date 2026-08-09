import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'

import { formatPrice, getProduct, imageUrl, StorefrontError } from '../lib/storefront.js'
import { useBasket } from '../lib/basket.js'
import { Alert, Button, Spinner } from '../components/ui.js'

/**
 * One product.
 *
 * The interesting part is the attribute table: everything a merchant added to
 * its OWN subtype is rendered here, keyed by its internal name, without this
 * page knowing a single one of those names in advance.
 */
export default function ProductPage() {
  const { entityId = '' } = useParams()
  const basket = useBasket()
  const product = useQuery({
    queryKey: ['product', entityId],
    queryFn: () => getProduct(entityId),
    retry: false,
  })

  if (product.isPending) return <Spinner label="Loading the product" />
  if (product.isError) {
    const notFound = product.error instanceof StorefrontError && product.error.status === 404
    return (
      <div className="space-y-4">
        <Alert>
          {notFound
            ? 'No such product. A draft or a withdrawn product is a 404 here, not a hidden field.'
            : String(product.error)}
        </Alert>
        <Link to="/" className="text-sm underline">
          Back to the catalog
        </Link>
      </div>
    )
  }

  const item = product.data
  const extras = Object.entries(item.attributes ?? {})

  return (
    <article className="grid gap-8 lg:grid-cols-2">
      <div className="aspect-square overflow-hidden rounded-lg bg-slate-100">
        {item.image !== undefined && item.image !== null ? (
          <img src={imageUrl(item)} alt={item.name} className="size-full object-cover" />
        ) : (
          <div className="flex size-full items-center justify-center text-sm text-slate-400">No photo</div>
        )}
      </div>

      <div className="space-y-5">
        <div>
          <h1 className="text-2xl font-semibold">{item.name}</h1>
          <p className="text-sm text-slate-500">
            {item.merchant} — <code className="rounded bg-slate-100 px-1">{item.subtype}</code>
          </p>
        </div>

        <p className="text-xl">{formatPrice(item)}</p>
        {item.description !== '' && <p className="text-sm leading-relaxed text-slate-700">{item.description}</p>}

        <Button
          type="button"
          onClick={() =>
            basket.add({
              entityId: item.entity_id,
              name: item.name,
              merchant: item.merchant,
              ...(item.price !== undefined ? { price: item.price } : {}),
              currency: item.currency,
            })
          }
        >
          Add to basket
        </Button>

        <dl className="divide-y divide-slate-100 border-t border-slate-200 text-sm">
          <Row label="SKU" value={item.sku} />
          <Row label="In stock" value={item.in_stock === undefined ? '' : item.in_stock ? 'Yes' : 'No'} />
          {extras.map(([name, value]) => (
            <Row key={name} label={name} value={renderValue(value)} />
          ))}
        </dl>

        {extras.length > 0 && (
          <p className="text-xs text-slate-500">
            The rows above the line are the fields every merchant shares. The rest are this merchant's own,
            projected under their internal names — this page knows none of them.
          </p>
        )}
      </div>
    </article>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  if (value === '') return null
  return (
    <div className="flex justify-between gap-4 py-2">
      <dt className="text-slate-500">{label}</dt>
      <dd className="text-right">{value}</dd>
    </div>
  )
}

/** Renders a projected value. A scoped value arrives keyed `name@fr`. */
function renderValue(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'boolean') return value ? 'Yes' : 'No'
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

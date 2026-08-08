import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Search } from 'lucide-react'

import { listMerchants, searchProducts, type SearchFilters } from '../lib/storefront.js'
import { ProductCard } from '../components/ProductCard.js'
import { Alert, Button, Spinner, TextInput } from '../components/ui.js'

const PAGE_SIZE = 24

/**
 * The shopper's catalog: every merchant, in one grid.
 *
 * The search is full text over the projection's generated `tsvector`, so it
 * covers a merchant's own product copy without the shop knowing any merchant's
 * schema.
 */
export default function BrowsePage() {
  const [draft, setDraft] = useState<SearchFilters>({})
  const [filters, setFilters] = useState<SearchFilters>({})
  const [page, setPage] = useState(0)

  const merchants = useQuery({ queryKey: ['merchants'], queryFn: listMerchants })
  const query = { ...filters, limit: PAGE_SIZE, offset: page * PAGE_SIZE }
  const products = useQuery({
    queryKey: ['products', query],
    queryFn: () => searchProducts(query),
  })

  function apply(event: React.FormEvent) {
    event.preventDefault()
    setPage(0)
    setFilters(draft)
  }

  return (
    <div className="space-y-8">
      <form onSubmit={apply} className="grid gap-3 sm:grid-cols-[1fr_180px_120px_120px_auto] sm:items-end">
        <label className="block text-sm">
          <span className="mb-1 block font-medium text-slate-700">Search</span>
          <TextInput
            value={draft.q ?? ''}
            placeholder="merino, roast, 240 V…"
            onChange={(event) => setDraft({ ...draft, q: event.target.value })}
          />
        </label>

        <label className="block text-sm">
          <span className="mb-1 block font-medium text-slate-700">Merchant</span>
          <select
            value={draft.merchant ?? ''}
            onChange={(event) => setDraft({ ...draft, merchant: event.target.value })}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
          >
            <option value="">Every merchant</option>
            {(merchants.data ?? []).map((merchant) => (
              <option key={merchant.tenant} value={merchant.tenant}>
                {merchant.display_name}
              </option>
            ))}
          </select>
        </label>

        <label className="block text-sm">
          <span className="mb-1 block font-medium text-slate-700">Min price</span>
          <TextInput
            inputMode="decimal"
            value={draft.minPrice ?? ''}
            onChange={(event) => setDraft({ ...draft, minPrice: event.target.value })}
          />
        </label>

        <label className="block text-sm">
          <span className="mb-1 block font-medium text-slate-700">Max price</span>
          <TextInput
            inputMode="decimal"
            value={draft.maxPrice ?? ''}
            onChange={(event) => setDraft({ ...draft, maxPrice: event.target.value })}
          />
        </label>

        <Button type="submit" className="flex items-center gap-2">
          <Search className="size-4" aria-hidden />
          Search
        </Button>
      </form>

      {products.isPending && <Spinner label="Loading the catalog" />}
      {products.isError && <Alert>{String(products.error)}</Alert>}
      {products.data?.length === 0 && <p className="text-sm text-slate-500">Nothing matched.</p>}

      <ul className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {(products.data ?? []).map((product) => (
          <ProductCard key={`${product.tenant}/${product.entity_id}`} product={product} />
        ))}
      </ul>

      <div className="flex items-center gap-3">
        <Button type="button" disabled={page === 0} onClick={() => setPage((current) => current - 1)}>
          Previous
        </Button>
        <Button
          type="button"
          disabled={(products.data?.length ?? 0) < PAGE_SIZE}
          onClick={() => setPage((current) => current + 1)}
        >
          Next
        </Button>
        <span className="text-xs text-slate-500">page {page + 1}</span>
      </div>
    </div>
  )
}

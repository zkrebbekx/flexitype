import { Link } from 'react-router-dom'

import { useBasket } from '../lib/basket.js'
import { Button } from '../components/ui.js'

/**
 * The basket.
 *
 * One storefront is one merchant, so a basket is one merchant's order. The
 * example stops here rather than pretending to settle payment.
 */
export default function BasketPage() {
  const { lines, remove, clear } = useBasket()

  if (lines.length === 0) {
    return (
      <div className="space-y-4">
        <p className="text-sm text-slate-500">The basket is empty.</p>
        <Link to="/" className="text-sm underline">
          Back to the catalog
        </Link>
      </div>
    )
  }

  const byMerchant = new Map<string, typeof lines>()
  for (const line of lines) {
    byMerchant.set(line.merchant, [...(byMerchant.get(line.merchant) ?? []), line])
  }

  return (
    <div className="space-y-8">
      {[...byMerchant.entries()].map(([merchant, merchantLines]) => (
        <section key={merchant} className="rounded-lg border border-slate-200 p-5">
          <h2 className="mb-3 text-sm font-semibold text-slate-700">{merchant}</h2>
          <ul className="divide-y divide-slate-100">
            {merchantLines.map((line) => (
              <li key={line.entityId} className="flex items-center justify-between py-3">
                <div>
                  <Link
                    to={`/p/${encodeURIComponent(line.entityId)}`}
                    className="text-sm font-medium hover:underline"
                  >
                    {line.name}
                  </Link>
                  <p className="text-xs text-slate-500">
                    {line.quantity} × {line.price ?? '—'} {line.currency}
                  </p>
                </div>
                <button
                  type="button"
                  className="text-xs text-slate-500 underline"
                  onClick={() => remove(line)}
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}

      <div className="flex items-center gap-4">
        <Button type="button" onClick={clear}>
          Empty the basket
        </Button>
        <p className="text-xs text-slate-500">
          One basket becomes one order per merchant, because a merchant is a tenant. Checkout is out of scope
          for this example.
        </p>
      </div>
    </div>
  )
}

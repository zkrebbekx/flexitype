import { Link } from 'react-router-dom'

import { formatPrice, imageUrl, type Product } from '../lib/storefront.js'

/** One product in the grid. */
export function ProductCard({ product }: { product: Product }) {
  return (
    <li className="rounded-lg border border-slate-200">
      <Link
        to={`/p/${encodeURIComponent(product.entity_id)}`}
        className="block"
      >
        <div className="aspect-square overflow-hidden rounded-t-lg bg-slate-100">
          {product.image !== undefined && product.image !== null ? (
            <img
              src={imageUrl(product)}
              alt={product.name}
              loading="lazy"
              className="size-full object-cover"
            />
          ) : (
            <div className="flex size-full items-center justify-center text-xs text-slate-400">No photo</div>
          )}
        </div>
        <div className="space-y-1 p-4">
          <p className="text-sm font-medium">{product.name}</p>
          <p className="text-xs text-slate-500">{product.merchant}</p>
          <p className="text-sm">{formatPrice(product)}</p>
          {product.in_stock === false && <p className="text-xs text-amber-700">Out of stock</p>}
        </div>
      </Link>
    </li>
  )
}

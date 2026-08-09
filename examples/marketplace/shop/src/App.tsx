import { Link, Route, Routes } from 'react-router-dom'
import { ShoppingBag } from 'lucide-react'

import BrowsePage from './pages/BrowsePage.js'
import ProductPage from './pages/ProductPage.js'
import BasketPage from './pages/BasketPage.js'
import { useBasket } from './lib/basket.js'

/** The shopper-facing routes. */
export default function App() {
  const { count } = useBasket()

  return (
    <div className="min-h-screen">
      <header className="border-b border-slate-200">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4">
          <Link to="/" className="text-lg font-semibold">
            The Marketplace
          </Link>
          <Link to="/basket" className="flex items-center gap-2 text-sm text-slate-600 hover:text-slate-900">
            <ShoppingBag className="size-4" aria-hidden />
            Basket
            <span className="rounded-full bg-slate-900 px-2 py-0.5 text-xs text-white">{count}</span>
          </Link>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-6 py-8">
        <Routes>
          <Route path="/" element={<BrowsePage />} />
          <Route path="/p/:entityId" element={<ProductPage />} />
          <Route path="/basket" element={<BasketPage />} />
          <Route path="*" element={<BrowsePage />} />
        </Routes>
      </main>

      <footer className="mx-auto max-w-6xl px-6 py-10 text-xs text-slate-500">
        This storefront serves one merchant. Its catalogue is a projection fed by that merchant's webhooks,
        because flexitype takes the tenant from the token — so a catalogue read is a projection, not a live
        query.
      </footer>
    </div>
  )
}

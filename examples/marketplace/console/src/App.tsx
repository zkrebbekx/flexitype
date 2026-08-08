import { Link, Navigate, Route, Routes, useParams } from 'react-router-dom'
import { Store } from 'lucide-react'

import MerchantsPage from './pages/MerchantsPage.js'
import MerchantLayout from './pages/MerchantLayout.js'
import SchemaPage from './pages/SchemaPage.js'
import ProductsPage from './pages/ProductsPage.js'
import ProductEditorPage from './pages/ProductEditorPage.js'

/** The route table. A merchant id in the path is the console's tenant. */
export default function App() {
  return (
    <div className="min-h-screen">
      <header className="border-b border-slate-200 bg-white">
        <div className="mx-auto flex max-w-6xl items-center gap-3 px-6 py-4">
          <Store className="size-5 text-slate-500" aria-hidden />
          <Link to="/" className="font-semibold">
            Merchant console
          </Link>
          <span className="text-sm text-slate-500">flexitype marketplace example</span>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-6 py-8">
        <Routes>
          <Route path="/" element={<MerchantsPage />} />
          <Route path="/m/:merchantId" element={<MerchantLayout />}>
            <Route index element={<RedirectToProducts />} />
            <Route path="products" element={<ProductsPage />} />
            <Route path="products/:entityId" element={<ProductEditorPage />} />
            <Route path="schema" element={<SchemaPage />} />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}

function RedirectToProducts() {
  const { merchantId } = useParams()
  return <Navigate to={`/m/${merchantId ?? ''}/products`} replace />
}

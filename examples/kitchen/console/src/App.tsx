import { Link, NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { CookingPot } from 'lucide-react'

import DishesPage from './pages/DishesPage.js'
import RecipePage from './pages/RecipePage.js'
import IngredientsPage from './pages/IngredientsPage.js'
import MenuChangesPage from './pages/MenuChangesPage.js'

/** The console's routes. */
export default function App() {
  return (
    <div className="min-h-screen">
      <header className="border-b border-stone-200 bg-white">
        <div className="mx-auto flex max-w-6xl items-center gap-6 px-6 py-4">
          <Link to="/" className="flex items-center gap-2 font-semibold">
            <CookingPot className="size-5 text-stone-500" aria-hidden />
            Kitchen
          </Link>
          <nav className="flex gap-1 text-sm">
            <Tab to="/dishes">Dishes</Tab>
            <Tab to="/ingredients">Ingredients</Tab>
            <Tab to="/menu-changes">Menu changes</Tab>
          </nav>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-6 py-8">
        <Routes>
          <Route path="/" element={<Navigate to="/dishes" replace />} />
          <Route path="/dishes" element={<DishesPage />} />
          <Route path="/dishes/:dishID" element={<RecipePage />} />
          <Route path="/ingredients" element={<IngredientsPage />} />
          <Route path="/menu-changes" element={<MenuChangesPage />} />
          <Route path="*" element={<Navigate to="/dishes" replace />} />
        </Routes>
      </main>

      <footer className="mx-auto max-w-6xl px-6 py-10 text-xs text-stone-500">
        Every cost on this page is read from flexitype, not calculated here. A supplier price change reaches
        a dish through two relationships, with nothing in this application to make it happen.
      </footer>
    </div>
  )
}

function Tab({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        `rounded px-3 py-1.5 font-medium ${
          isActive ? 'bg-stone-900 text-white' : 'text-stone-600 hover:bg-stone-100'
        }`
      }
    >
      {children}
    </NavLink>
  )
}

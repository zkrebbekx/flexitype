import { NavLink, Outlet, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { FlexitypeProvider } from '@flexitype/client/react'

import { listMerchants } from '../lib/platform.js'
import { merchantClient } from '../lib/merchantClient.js'
import { Alert, Spinner } from '../components/ui.js'

/**
 * Everything under /m/:merchantId.
 *
 * One provider is one tenant, because the tenant travels in the token. The
 * client is keyed by merchant id, so switching merchants swaps the client and
 * every hook below re-reads against the new tenant.
 */
export default function MerchantLayout() {
  const { merchantId = '' } = useParams()
  const merchants = useQuery({ queryKey: ['merchants'], queryFn: listMerchants })
  const merchant = merchants.data?.find((m) => m.id === merchantId)

  if (merchants.isPending) return <Spinner label="Loading merchants" />
  if (merchants.isError) return <Alert>{String(merchants.error)}</Alert>
  if (merchant === undefined) return <Alert>No merchant called “{merchantId}”.</Alert>

  return (
    <FlexitypeProvider client={merchantClient(merchantId)}>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-semibold">{merchant.display_name}</h1>
          <p className="text-sm text-slate-500">
            tenant <code className="rounded bg-slate-100 px-1">{merchant.tenant}</code> — its own schema, its
            own products
          </p>
        </div>

        <nav className="flex gap-1 border-b border-slate-200">
          <Tab to={`/m/${merchantId}/products`}>Products</Tab>
          <Tab to={`/m/${merchantId}/schema`}>Schema</Tab>
        </nav>

        <Outlet />
      </div>
    </FlexitypeProvider>
  )
}

function Tab({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        `-mb-px border-b-2 px-4 py-2 text-sm font-medium ${
          isActive ? 'border-slate-900 text-slate-900' : 'border-transparent text-slate-500 hover:text-slate-800'
        }`
      }
    >
      {children}
    </NavLink>
  )
}

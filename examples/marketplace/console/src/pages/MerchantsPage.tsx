import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { listMerchants, onboardMerchant, PlatformError } from '../lib/platform.js'
import { Alert, Button, Card, Notice, Spinner, TextInput } from '../components/ui.js'

/**
 * Every merchant, and the form that onboards one.
 *
 * Onboarding is a platform call, not a flexitype call: it creates the TENANT
 * and the service account, applies the `ecommerce` starter schema, registers
 * the storefront's webhook and triggers the first backfill. It is idempotent,
 * so a second submit repairs a part-way failure instead of erroring.
 */
export default function MerchantsPage() {
  const queryClient = useQueryClient()
  const merchants = useQuery({ queryKey: ['merchants'], queryFn: listMerchants })
  const [id, setId] = useState('')
  const [displayName, setDisplayName] = useState('')

  const onboard = useMutation({
    mutationFn: () => onboardMerchant({ id: id.trim(), display_name: displayName.trim(), tenant: id.trim() }),
    onSuccess: async () => {
      setId('')
      setDisplayName('')
      await queryClient.invalidateQueries({ queryKey: ['merchants'] })
    },
  })

  return (
    <div className="space-y-8">
      <Card title="Merchants">
        {merchants.isPending && <Spinner label="Loading merchants" />}
        {merchants.isError && <Alert>{String(merchants.error)}</Alert>}
        {merchants.data?.length === 0 && (
          <p className="text-sm text-slate-500">No merchants yet. Onboard one below.</p>
        )}
        <ul className="divide-y divide-slate-100">
          {(merchants.data ?? []).map((merchant) => (
            <li key={merchant.id} className="flex items-center justify-between py-3">
              <div>
                <Link to={`/m/${merchant.id}/products`} className="font-medium hover:underline">
                  {merchant.display_name}
                </Link>
                <p className="text-xs text-slate-500">
                  tenant <code className="rounded bg-slate-100 px-1">{merchant.tenant}</code>
                </p>
              </div>
              <Link to={`/m/${merchant.id}/schema`} className="text-sm text-slate-500 hover:underline">
                Schema
              </Link>
            </li>
          ))}
        </ul>
      </Card>

      <Card title="Onboard a merchant">
        <form
          className="grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
          onSubmit={(event) => {
            event.preventDefault()
            onboard.mutate()
          }}
        >
          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">Merchant id</span>
            <TextInput
              value={id}
              required
              pattern="[a-z0-9][a-z0-9-]*"
              placeholder="alpine"
              onChange={(event) => setId(event.target.value)}
            />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">Display name</span>
            <TextInput
              value={displayName}
              required
              placeholder="Alpine Apparel"
              onChange={(event) => setDisplayName(event.target.value)}
            />
          </label>
          <Button type="submit" disabled={onboard.isPending}>
            {onboard.isPending ? 'Onboarding…' : 'Onboard'}
          </Button>
        </form>

        {onboard.isError && (
          <div className="mt-4">
            <Alert>
              {onboard.error instanceof PlatformError ? onboard.error.message : String(onboard.error)}
            </Alert>
          </div>
        )}
        {onboard.isSuccess && (
          <div className="mt-4">
            <Notice>
              Onboarded. The merchant has its own tenant, the starter schema and a webhook subscription.
            </Notice>
          </div>
        )}
      </Card>
    </div>
  )
}

import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { CHANNELS, listDishes, listMenuChanges, money, scheduleMenuChange } from '../lib/kitchen.js'
import { Alert, Button, Card, Notice, Spinner, TextInput } from '../components/ui.js'

/**
 * Next week's prices, staged and scheduled.
 *
 * Writing each price at the moment it should take effect needs somebody awake
 * at 06:00 on Monday. A change set is approved ahead of time and published by
 * the service, and every price in it moves in ONE transaction — a menu is
 * never half-changed.
 */
export default function MenuChangesPage() {
  const queryClient = useQueryClient()
  const changes = useQuery({ queryKey: ['menu-changes'], queryFn: listMenuChanges })
  const dishes = useQuery({ queryKey: ['dishes'], queryFn: listDishes })

  const [name, setName] = useState('')
  const [publishAt, setPublishAt] = useState('')
  const [prices, setPrices] = useState<Record<string, Record<string, string>>>({})

  const schedule = useMutation({
    mutationFn: () =>
      scheduleMenuChange({
        name: name.trim(),
        ...(publishAt === '' ? {} : { publish_at: new Date(publishAt).toISOString() }),
        prices: Object.fromEntries(
          Object.entries(prices)
            .map(([dishID, byChannel]) => [
              dishID,
              Object.fromEntries(Object.entries(byChannel).filter(([, value]) => value !== '')),
            ])
            .filter(([, byChannel]) => Object.keys(byChannel as object).length > 0),
        ),
      }),
    onSuccess: async () => {
      setName('')
      setPublishAt('')
      setPrices({})
      await queryClient.invalidateQueries()
    },
  })

  return (
    <div className="space-y-6">
      <Card title="Scheduled changes">
        {changes.isPending && <Spinner label="Loading" />}
        {changes.isError && <Alert>{String(changes.error)}</Alert>}
        {changes.data?.length === 0 && <p className="text-sm text-stone-500">Nothing staged.</p>}
        <ul className="divide-y divide-stone-100">
          {(changes.data ?? []).map((change) => (
            <li key={change.id} className="flex items-center justify-between py-2 text-sm">
              <span className="font-medium">{change.name}</span>
              <span className="text-stone-500">
                {change.state}
                {change.publish_at !== undefined && change.publish_at !== ''
                  ? ` — ${new Date(change.publish_at).toLocaleString()}`
                  : ''}
              </span>
            </li>
          ))}
        </ul>
      </Card>

      <Card title="Stage a price change">
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            schedule.mutate()
          }}
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-sm">
              <span className="mb-1 block text-xs text-stone-500">Name</span>
              <TextInput
                aria-label="Name"
                value={name}
                required
                placeholder="Autumn prices"
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            <label className="block text-sm">
              <span className="mb-1 block text-xs text-stone-500">Publish at (empty publishes now)</span>
              <TextInput
                aria-label="Publish at"
                type="datetime-local"
                value={publishAt}
                onChange={(event) => setPublishAt(event.target.value)}
              />
            </label>
          </div>

          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs uppercase text-stone-500">
                <th className="py-1">Dish</th>
                {CHANNELS.map((channel) => (
                  <th key={channel} className="py-1">
                    {channel.replace('_', ' ')}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-stone-100">
              {(dishes.data ?? []).map((dish) => (
                <tr key={dish.id}>
                  <td className="py-2">{dish.name[''] ?? dish.id}</td>
                  {CHANNELS.map((channel) => (
                    <td key={channel} className="py-2">
                      <TextInput
                        aria-label={`${dish.id} ${channel}`}
                        inputMode="decimal"
                        className="w-24"
                        placeholder={money(dish.price[channel])}
                        value={prices[dish.id]?.[channel] ?? ''}
                        onChange={(event) =>
                          setPrices({
                            ...prices,
                            [dish.id]: { ...(prices[dish.id] ?? {}), [channel]: event.target.value },
                          })
                        }
                      />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>

          <Button type="submit" disabled={schedule.isPending}>
            {schedule.isPending ? 'Staging…' : 'Stage the change'}
          </Button>
        </form>

        {schedule.isError && (
          <div className="mt-3">
            <Alert>{String(schedule.error)}</Alert>
          </div>
        )}
        {schedule.isSuccess && (
          <div className="mt-3">
            <Notice>
              Staged. With a time it waits, approved, until the service publishes it; with none it is live
              already.
            </Notice>
          </div>
        )}
      </Card>
    </div>
  )
}

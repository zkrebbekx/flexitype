import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { CHANNELS, asPercent, listDishes, money, putDish } from '../lib/kitchen.js'
import { Alert, Button, Card, Derived, Spinner, TextInput } from '../components/ui.js'

/** Every dish, with the cost and margin the service derived. */
export default function DishesPage() {
  const queryClient = useQueryClient()
  const dishes = useQuery({ queryKey: ['dishes'], queryFn: listDishes })
  const [id, setId] = useState('')
  const [name, setName] = useState('')

  const create = useMutation({
    mutationFn: () => putDish(id.trim(), { name: { '': name.trim() }, course: 'main' }),
    onSuccess: async () => {
      setId('')
      setName('')
      await queryClient.invalidateQueries({ queryKey: ['dishes'] })
    },
  })

  return (
    <div className="space-y-6">
      <Card title="Dishes">
        {dishes.isPending && <Spinner label="Loading dishes" />}
        {dishes.isError && <Alert>{String(dishes.error)}</Alert>}
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs uppercase text-stone-500">
              <th className="py-1">Dish</th>
              <th className="py-1">Lines</th>
              <th className="py-1">Food cost</th>
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
                <td className="py-2">
                  <Link to={`/dishes/${encodeURIComponent(dish.id)}`} className="font-medium hover:underline">
                    {dish.name[''] ?? dish.id}
                  </Link>
                  <span className="ml-2 text-xs text-stone-500">{dish.status ?? 'draft'}</span>
                </td>
                <td className="py-2 text-stone-600">{dish.line_count}</td>
                <td className="py-2">
                  <Derived>{money(dish.food_cost)}</Derived>
                </td>
                {CHANNELS.map((channel) => (
                  <td key={channel} className="py-2 text-stone-600">
                    {money(dish.price[channel])}
                    <span className="ml-1 text-xs text-stone-400">{asPercent(dish.margin?.[channel])}</span>
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      <Card title="Add a dish">
        <form
          className="grid gap-3 sm:grid-cols-[200px_1fr_auto] sm:items-end"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <label className="block text-sm">
            <span className="mb-1 block text-xs text-stone-500">Id</span>
            <TextInput value={id} required placeholder="tart" onChange={(e) => setId(e.target.value)} />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-xs text-stone-500">Name</span>
            <TextInput
              value={name}
              required
              placeholder="Chocolate tart"
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <Button type="submit" disabled={create.isPending}>
            Add
          </Button>
        </form>
        {create.isError && (
          <div className="mt-3">
            <Alert>{String(create.error)}</Alert>
          </div>
        )}
      </Card>
    </div>
  )
}

import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { listIngredients, money, putIngredient } from '../lib/kitchen.js'
import { Alert, Button, Card, Derived, Spinner, TextInput } from '../components/ui.js'

/**
 * What the kitchen buys, and what one kilogram of it costs.
 *
 * The cost per kilogram is DERIVED: pack price divided by pack size, in the
 * unit family's base unit. A pack bought in pounds and one bought in grams
 * both land on a price per kilogram with no conversion in this console.
 */
export default function IngredientsPage() {
  const queryClient = useQueryClient()
  const ingredients = useQuery({ queryKey: ['ingredients'], queryFn: listIngredients })
  const [form, setForm] = useState({ id: '', name: '', supplier: '', magnitude: '', unit: 'kg', price: '' })

  const save = useMutation({
    mutationFn: () =>
      putIngredient(form.id.trim(), {
        name: form.name.trim(),
        supplier: form.supplier.trim(),
        pack_size: { magnitude: form.magnitude.trim(), unit: form.unit },
        pack_price: form.price.trim(),
      }),
    onSuccess: async () => {
      setForm({ id: '', name: '', supplier: '', magnitude: '', unit: 'kg', price: '' })
      // A price change recosts every dish that uses it, so both lists move.
      await queryClient.invalidateQueries()
    },
  })

  return (
    <div className="space-y-6">
      <Card title="Ingredients">
        {ingredients.isPending && <Spinner label="Loading ingredients" />}
        {ingredients.isError && <Alert>{String(ingredients.error)}</Alert>}
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs uppercase text-stone-500">
              <th className="py-1">Ingredient</th>
              <th className="py-1">Supplier</th>
              <th className="py-1">Pack</th>
              <th className="py-1">Pack price</th>
              <th className="py-1">Cost per kg</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {(ingredients.data ?? []).map((ingredient) => (
              <tr key={ingredient.id}>
                <td className="py-2 font-medium">{ingredient.name}</td>
                <td className="py-2 text-stone-600">{ingredient.supplier ?? '—'}</td>
                <td className="py-2 text-stone-600">
                  {ingredient.pack_size
                    ? `${ingredient.pack_size.magnitude} ${ingredient.pack_size.unit}`
                    : '—'}
                </td>
                <td className="py-2 text-stone-600">{money(ingredient.pack_price)}</td>
                <td className="py-2">
                  <Derived title="pack price ÷ pack size, in the family's base unit">
                    {money(ingredient.cost_per_kg)}
                  </Derived>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      <Card title="Add or reprice an ingredient">
        <form
          className="grid gap-3 sm:grid-cols-6 sm:items-end"
          onSubmit={(event) => {
            event.preventDefault()
            save.mutate()
          }}
        >
          <Field label="Id" value={form.id} onChange={(v) => setForm({ ...form, id: v })} required />
          <Field label="Name" value={form.name} onChange={(v) => setForm({ ...form, name: v })} required />
          <Field label="Supplier" value={form.supplier} onChange={(v) => setForm({ ...form, supplier: v })} />
          <Field
            label="Pack size"
            value={form.magnitude}
            onChange={(v) => setForm({ ...form, magnitude: v })}
            required
          />
          <label className="block text-sm">
            <span className="mb-1 block text-xs text-stone-500">Unit</span>
            <select
              aria-label="Unit"
              value={form.unit}
              onChange={(event) => setForm({ ...form, unit: event.target.value })}
              className="w-full rounded border border-stone-300 bg-white px-2 py-1.5 text-sm"
            >
              {['kg', 'g', 'lb', 'oz'].map((unit) => (
                <option key={unit} value={unit}>
                  {unit}
                </option>
              ))}
            </select>
          </label>
          <Field
            label="Pack price"
            value={form.price}
            onChange={(v) => setForm({ ...form, price: v })}
            required
          />
          <div className="sm:col-span-6">
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? 'Saving…' : 'Save'}
            </Button>
          </div>
        </form>
        {save.isError && (
          <div className="mt-3">
            <Alert>{String(save.error)}</Alert>
          </div>
        )}
      </Card>
    </div>
  )
}

function Field({
  label,
  value,
  onChange,
  required = false,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  required?: boolean
}) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block text-xs text-stone-500">{label}</span>
      <TextInput
        aria-label={label}
        value={value}
        required={required}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  )
}

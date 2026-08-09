import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trash2 } from 'lucide-react'

import {
  CHANNELS,
  KitchenError,
  LOCALES,
  asPercent,
  deleteLine,
  getDish,
  listIngredients,
  money,
  publishDish,
  putDish,
  putLine,
} from '../lib/kitchen.js'
import { Alert, Button, Card, Derived, Notice, SecondaryButton, Spinner, TextInput } from '../components/ui.js'

/**
 * One dish: its recipe, its cost, its prices and its allergens.
 *
 * The costs on this page are READ. Change a quantity and the line cost and the
 * dish's food cost both move — because the service recomputed them, not
 * because anything here did.
 */
export default function RecipePage() {
  const { dishID = '' } = useParams()
  const queryClient = useQueryClient()
  const dish = useQuery({ queryKey: ['dish', dishID], queryFn: () => getDish(dishID) })
  const ingredients = useQuery({ queryKey: ['ingredients'], queryFn: listIngredients })

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['dish', dishID] })
    await queryClient.invalidateQueries({ queryKey: ['dishes'] })
  }

  const saveLine = useMutation({
    mutationFn: (input: { lineID: string; ingredientID: string; magnitude: string; unit: string }) =>
      putLine(dishID, input.lineID, {
        ingredient_id: input.ingredientID,
        quantity: { magnitude: input.magnitude, unit: input.unit },
      }),
    onSuccess: refresh,
  })
  const removeLine = useMutation({
    mutationFn: (lineID: string) => deleteLine(dishID, lineID),
    onSuccess: refresh,
  })
  const savePrices = useMutation({
    mutationFn: (price: Record<string, string>) => putDish(dishID, { price }),
    onSuccess: refresh,
  })
  const publish = useMutation({ mutationFn: () => publishDish(dishID), onSuccess: refresh })

  if (dish.isPending) return <Spinner label="Loading the dish" />
  if (dish.isError) return <Alert>{String(dish.error)}</Alert>

  const item = dish.data

  return (
    <div className="space-y-6">
      <div className="flex items-baseline justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{item.name[''] ?? item.id}</h1>
          <p className="text-sm text-stone-500">
            {item.course ?? 'uncategorised'} — <code>{item.status ?? 'draft'}</code>
          </p>
        </div>
        <Link to="/dishes" className="text-sm text-stone-500 hover:underline">
          All dishes
        </Link>
      </div>

      <Card title="Recipe">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs uppercase text-stone-500">
              <th className="py-1">Ingredient</th>
              <th className="py-1">Quantity</th>
              <th className="py-1">Cost per kg</th>
              <th className="py-1">Line cost</th>
              <th className="py-1" />
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {(item.lines ?? []).map((line) => (
              <tr key={line.id}>
                <td className="py-2">{line.ingredient ?? line.ingredient_id}</td>
                <td className="py-2">
                  <QuantityEditor
                    label={`Quantity of ${line.ingredient ?? line.ingredient_id}`}
                    quantity={line.quantity}
                    disabled={saveLine.isPending}
                    onChange={(magnitude, unit) =>
                      saveLine.mutate({
                        lineID: line.id,
                        ingredientID: line.ingredient_id,
                        magnitude,
                        unit,
                      })
                    }
                  />
                </td>
                {/* Both of these came from the service. */}
                <td className="py-2">
                  <Derived title="Rolled up from the ingredient this line points at">
                    {money(line.cost_per_kg)}
                  </Derived>
                </td>
                <td className="py-2">
                  <Derived title="quantity × cost per kg, computed by the service">
                    {money(line.line_cost)}
                  </Derived>
                </td>
                <td className="py-2 text-right">
                  <button
                    type="button"
                    aria-label={`Remove ${line.ingredient ?? line.ingredient_id}`}
                    className="text-stone-400 hover:text-rose-600"
                    onClick={() => removeLine.mutate(line.id)}
                  >
                    <Trash2 className="size-4" aria-hidden />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr className="border-t border-stone-200">
              <td className="py-2 font-medium" colSpan={3}>
                Food cost ({item.line_count} {item.line_count === 1 ? 'line' : 'lines'})
              </td>
              <td className="py-2">
                <Derived title="A rollup over this dish's lines">{money(item.food_cost)}</Derived>
              </td>
              <td />
            </tr>
          </tfoot>
        </table>

        <div className="mt-5 border-t border-stone-100 pt-4">
          <AddLine
            ingredients={(ingredients.data ?? []).map((i) => ({ id: i.id, name: i.name }))}
            disabled={saveLine.isPending}
            onAdd={(ingredientID, magnitude, unit) =>
              saveLine.mutate({
                lineID: `${dishID}-${ingredientID}`,
                ingredientID,
                magnitude,
                unit,
              })
            }
          />
        </div>
      </Card>

      <Card title="Price, per channel">
        <PriceMatrix
          price={item.price}
          margin={item.margin}
          disabled={savePrices.isPending}
          onSave={(price) => savePrices.mutate(price)}
        />
        <p className="mt-3 text-xs text-stone-500">
          The margin is the one number this console computes. A formula reads the base value of its inputs,
          and a price scoped by channel has three — so the service refuses to pretend there is one answer.
        </p>
      </Card>

      <Card title="Names">
        <dl className="grid gap-2 text-sm sm:grid-cols-2">
          {LOCALES.map((locale) => (
            <div key={locale || 'base'} className="flex gap-2">
              <dt className="w-16 text-stone-500">{locale === '' ? 'base' : locale}</dt>
              <dd>{item.name[locale] ?? <span className="text-stone-400">not translated</span>}</dd>
            </div>
          ))}
        </dl>
      </Card>

      <Card title="On the menu">
        <div className="flex items-center gap-3">
          <Button type="button" disabled={publish.isPending} onClick={() => publish.mutate()}>
            {publish.isPending ? 'Publishing…' : 'Put on the menu'}
          </Button>
          <span className="text-xs text-stone-500">
            A dish reaches the menu only when the schema says it is complete.
          </span>
        </div>
        {publish.isError && (
          <div className="mt-3">
            <Alert>
              {publish.error instanceof KitchenError && publish.error.missing.length > 0
                ? `Not ready: ${publish.error.missing.join(', ')} still needed.`
                : String(publish.error)}
            </Alert>
          </div>
        )}
        {publish.isSuccess && (
          <div className="mt-3">
            <Notice>On the menu.</Notice>
          </div>
        )}
      </Card>
    </div>
  )
}

/** Edits one line's quantity, in whatever unit the cook thinks in. */
function QuantityEditor({
  label,
  quantity,
  disabled,
  onChange,
}: {
  label: string
  quantity: { magnitude: string; unit: string } | undefined
  disabled: boolean
  onChange: (magnitude: string, unit: string) => void
}) {
  const [magnitude, setMagnitude] = useState(quantity?.magnitude ?? '')
  const unit = quantity?.unit ?? 'g'

  return (
    <span className="flex items-center gap-1">
      <TextInput
        aria-label={label}
        inputMode="decimal"
        value={magnitude}
        disabled={disabled}
        className="w-24"
        onChange={(event) => setMagnitude(event.target.value)}
        onBlur={() => {
          if (magnitude !== '' && magnitude !== quantity?.magnitude) onChange(magnitude, unit)
        }}
      />
      <span className="text-stone-500">{unit}</span>
    </span>
  )
}

/** Adds a line to the recipe. */
function AddLine({
  ingredients,
  disabled,
  onAdd,
}: {
  ingredients: { id: string; name: string }[]
  disabled: boolean
  onAdd: (ingredientID: string, magnitude: string, unit: string) => void
}) {
  const [ingredientID, setIngredientID] = useState('')
  const [magnitude, setMagnitude] = useState('')
  const [unit, setUnit] = useState('g')

  return (
    <form
      className="grid gap-2 sm:grid-cols-[1fr_120px_100px_auto] sm:items-end"
      onSubmit={(event) => {
        event.preventDefault()
        if (ingredientID === '' || magnitude === '') return
        onAdd(ingredientID, magnitude, unit)
        setMagnitude('')
      }}
    >
      <label className="block text-sm">
        <span className="mb-1 block text-xs text-stone-500">Ingredient</span>
        <select
          aria-label="Ingredient"
          value={ingredientID}
          onChange={(event) => setIngredientID(event.target.value)}
          className="w-full rounded border border-stone-300 bg-white px-2 py-1.5 text-sm"
        >
          <option value="">Choose…</option>
          {ingredients.map((ingredient) => (
            <option key={ingredient.id} value={ingredient.id}>
              {ingredient.name}
            </option>
          ))}
        </select>
      </label>
      <label className="block text-sm">
        <span className="mb-1 block text-xs text-stone-500">Quantity</span>
        <TextInput
          aria-label="New quantity"
          inputMode="decimal"
          value={magnitude}
          onChange={(event) => setMagnitude(event.target.value)}
        />
      </label>
      <label className="block text-sm">
        <span className="mb-1 block text-xs text-stone-500">Unit</span>
        <select
          aria-label="Unit"
          value={unit}
          onChange={(event) => setUnit(event.target.value)}
          className="w-full rounded border border-stone-300 bg-white px-2 py-1.5 text-sm"
        >
          {['g', 'kg', 'lb', 'oz'].map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      </label>
      <SecondaryButton type="submit" disabled={disabled}>
        Add
      </SecondaryButton>
    </form>
  )
}

/** The price of one dish in every channel, with the margin each implies. */
function PriceMatrix({
  price,
  margin,
  disabled,
  onSave,
}: {
  price: Record<string, string>
  margin: Record<string, string> | undefined
  disabled: boolean
  onSave: (price: Record<string, string>) => void
}) {
  const [draft, setDraft] = useState<Record<string, string>>(price)

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault()
        onSave(draft)
      }}
    >
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-xs uppercase text-stone-500">
            <th className="py-1">Channel</th>
            <th className="py-1">Price</th>
            <th className="py-1">Margin</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-stone-100">
          {CHANNELS.map((channel) => (
            <tr key={channel}>
              <td className="py-2">{channel.replace('_', ' ')}</td>
              <td className="py-2">
                <TextInput
                  aria-label={`${channel} price`}
                  inputMode="decimal"
                  className="w-28"
                  value={draft[channel] ?? ''}
                  onChange={(event) => setDraft({ ...draft, [channel]: event.target.value })}
                />
              </td>
              <td className="py-2 text-stone-600">{asPercent(margin?.[channel])}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="mt-3">
        <Button type="submit" disabled={disabled}>
          {disabled ? 'Saving…' : 'Save prices'}
        </Button>
      </div>
    </form>
  )
}

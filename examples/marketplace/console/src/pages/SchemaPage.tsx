import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffectiveAttributes, useTypes } from '@flexitype/client/react'

import { createSubtype, PlatformError, type AttributeInput } from '../lib/platform.js'
import { Alert, Button, Card, Notice, SecondaryButton, Spinner, TextInput } from '../components/ui.js'

/** The data types a merchant can pick for a field of its own. */
const DATA_TYPES = ['string', 'integer', 'decimal', 'bool', 'enum', 'date', 'datetime', 'url', 'email', 'json']

/**
 * The merchant's schema: the types it has, and the subtypes it adds itself.
 *
 * The type list is read with the SDK's `useTypes` through the platform's
 * passthrough, so it is a real flexitype read. Creating a subtype is a
 * platform call, because it creates the type AND its attributes in one
 * request and is idempotent.
 */
export default function SchemaPage() {
  const { merchantId = '' } = useParams()
  const queryClient = useQueryClient()
  const types = useTypes({ limit: 100 })
  const [selected, setSelected] = useState<string>()

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <Card title="Types">
        {types.isPending && <Spinner label="Loading types" />}
        {types.isError && <Alert>{types.error.message}</Alert>}
        <ul className="divide-y divide-slate-100">
          {(types.data?.items ?? []).map((type) => (
            <li key={type.id} className="flex items-center justify-between py-2">
              <div>
                <p className="text-sm font-medium">{type.display_name}</p>
                <p className="text-xs text-slate-500">
                  <code>{type.internal_name}</code>
                  {type.extends_id !== undefined && type.extends_id !== '' && ' — a subtype'}
                </p>
              </div>
              <SecondaryButton type="button" onClick={() => setSelected(type.id)}>
                Fields
              </SecondaryButton>
            </li>
          ))}
        </ul>
      </Card>

      <div className="space-y-6">
        <NewSubtype
          merchantId={merchantId}
          onCreated={async () => {
            await queryClient.invalidateQueries()
          }}
        />
        {selected !== undefined && <EffectiveAttributes typeId={selected} />}
      </div>
    </div>
  )
}

/** A type's effective attributes: its own, plus everything it inherits. */
function EffectiveAttributes({ typeId }: { typeId: string }) {
  const attributes = useEffectiveAttributes(typeId)

  return (
    <Card title="Effective attributes">
      {attributes.isPending && <Spinner label="Loading attributes" />}
      {attributes.isError && <Alert>{attributes.error.message}</Alert>}
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-xs uppercase text-slate-500">
            <th className="py-1">Field</th>
            <th className="py-1">Type</th>
            <th className="py-1">Declared in</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-100">
          {(attributes.data ?? []).map((entry) => (
            <tr key={entry.attribute?.id ?? entry.attribute?.internal_name}>
              <td className="py-1.5">
                <code>{entry.attribute?.internal_name}</code>
                {entry.attribute?.required === true && <span className="ml-1 text-rose-600">*</span>}
              </td>
              <td className="py-1.5 text-slate-600">{entry.attribute?.data_type}</td>
              <td className="py-1.5 text-slate-500">
                {entry.declared_in?.display_name ?? entry.declared_in?.internal_name ?? '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  )
}

/** The form that adds a subtype with fields only this merchant has. */
function NewSubtype({ merchantId, onCreated }: { merchantId: string; onCreated: () => Promise<void> }) {
  const [internalName, setInternalName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [attributes, setAttributes] = useState<AttributeInput[]>([
    { internal_name: '', display_name: '', data_type: 'string' },
  ])

  const create = useMutation({
    mutationFn: () =>
      createSubtype(merchantId, {
        internal_name: internalName.trim(),
        display_name: displayName.trim(),
        attributes: attributes.filter((attribute) => attribute.internal_name.trim() !== ''),
      }),
    onSuccess: async () => {
      setInternalName('')
      setDisplayName('')
      setAttributes([{ internal_name: '', display_name: '', data_type: 'string' }])
      await onCreated()
    },
  })

  function update(index: number, patch: Partial<AttributeInput>) {
    setAttributes((current) => current.map((entry, i) => (i === index ? { ...entry, ...patch } : entry)))
  }

  return (
    <Card title="Add a subtype">
      <p className="mb-4 text-xs text-slate-500">
        A subtype inherits every product field — name, price, status — and adds the ones only this merchant
        has. The storefront still finds the inherited fields without knowing the subtype exists.
      </p>
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          create.mutate()
        }}
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">Internal name</span>
            <TextInput
              value={internalName}
              required
              placeholder="apparel"
              onChange={(event) => setInternalName(event.target.value)}
            />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">Display name</span>
            <TextInput
              value={displayName}
              required
              placeholder="Apparel"
              onChange={(event) => setDisplayName(event.target.value)}
            />
          </label>
        </div>

        <div className="space-y-2">
          <p className="text-sm font-medium text-slate-700">Its own fields</p>
          {attributes.map((attribute, index) => (
            <div key={index} className="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
              <TextInput
                aria-label={`Field ${index + 1} internal name`}
                value={attribute.internal_name}
                placeholder="size"
                onChange={(event) => update(index, { internal_name: event.target.value })}
              />
              <TextInput
                aria-label={`Field ${index + 1} display name`}
                value={attribute.display_name}
                placeholder="Size"
                onChange={(event) => update(index, { display_name: event.target.value })}
              />
              <select
                aria-label={`Field ${index + 1} data type`}
                value={attribute.data_type}
                onChange={(event) => update(index, { data_type: event.target.value })}
                className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
              >
                {DATA_TYPES.map((dataType) => (
                  <option key={dataType} value={dataType}>
                    {dataType}
                  </option>
                ))}
              </select>
            </div>
          ))}
          <SecondaryButton
            type="button"
            onClick={() =>
              setAttributes((current) => [
                ...current,
                { internal_name: '', display_name: '', data_type: 'string' },
              ])
            }
          >
            Add a field
          </SecondaryButton>
        </div>

        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? 'Creating…' : 'Create subtype'}
        </Button>

        {create.isError && (
          <Alert>{create.error instanceof PlatformError ? create.error.message : String(create.error)}</Alert>
        )}
        {create.isSuccess && <Notice>Subtype created. Its fields are on every product of that subtype.</Notice>}
      </form>
    </Card>
  )
}

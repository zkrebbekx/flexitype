import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEntityValues, useFormDescriptor, useTypes } from '@flexitype/client/react'

import { DynamicForm, toFormState, type FormState } from '../components/DynamicForm.js'
import { deleteProduct, putProduct, uploadImage, PlatformError } from '../lib/platform.js'
import { Alert, Card, Notice, SecondaryButton, Spinner, TextInput } from '../components/ui.js'

/**
 * One product, edited through a form the SCHEMA draws.
 *
 * The console names no product field. It reads the type's effective
 * attributes with the SDK, turns them into a form descriptor, and writes the
 * result back as one atomic batch. A merchant that adds `voltage` to its own
 * subtype gets a `voltage` input here with no change to this file.
 */
export default function ProductEditorPage() {
  const { merchantId = '', entityId = '' } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const isNew = entityId === 'new'
  const [typeName, setTypeName] = useState(search.get('type') ?? 'product')
  const [newEntityId, setNewEntityId] = useState('')
  const [locale, setLocale] = useState('')
  const [state, setState] = useState<FormState>({})

  const types = useTypes({ limit: 100 })
  const typeId = useMemo(
    () => types.data?.items?.find((entry) => entry.internal_name === typeName)?.id,
    [types.data, typeName],
  )

  const form = useFormDescriptor(typeId)
  const values = useEntityValues(isNew ? undefined : typeId, isNew ? undefined : entityId)

  // The editor state is seeded once the schema and the values are both in.
  useEffect(() => {
    if (isNew || form.descriptor === undefined || values.data === undefined) return
    const byName: Record<string, unknown> = {}
    for (const value of values.data) {
      const field = form.descriptor.byId[value.attribute_definition_id ?? '']
      if (field === undefined) continue
      // A localized value is shown when its locale is the one being edited.
      if ((value.locale ?? '') !== locale) continue
      byName[field.name] = value.value
    }
    setState(toFormState(form.descriptor, byName))
  }, [isNew, form.descriptor, values.data, locale])

  const save = useMutation({
    mutationFn: async (wire: Record<string, unknown>) => {
      const target = isNew ? newEntityId.trim() : entityId
      if (target === '') throw new PlatformError(400, 'Give the product an id.')
      await putProduct(merchantId, target, { type: typeName, values: wire })
      return target
    },
    onSuccess: async (target) => {
      await queryClient.invalidateQueries()
      if (isNew) navigate(`/m/${merchantId}/products/${encodeURIComponent(target)}?type=${typeName}`)
    },
  })

  const archive = useMutation({
    mutationFn: () => deleteProduct(merchantId, entityId, typeName),
    onSuccess: async () => {
      await queryClient.invalidateQueries()
      navigate(`/m/${merchantId}/products`)
    },
  })

  const upload = useMutation({
    mutationFn: (file: File) => uploadImage(merchantId, entityId, typeName, file),
    onSuccess: async () => {
      await queryClient.invalidateQueries()
    },
  })

  return (
    <div className="space-y-6">
      <Card title={isNew ? 'New product' : `Product ${entityId}`}>
        <div className="mb-6 grid gap-3 sm:grid-cols-3">
          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">Type</span>
            <select
              value={typeName}
              onChange={(event) => setTypeName(event.target.value)}
              disabled={!isNew}
              className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
            >
              {(types.data?.items ?? []).map((entry) => (
                <option key={entry.id} value={entry.internal_name}>
                  {entry.display_name}
                </option>
              ))}
            </select>
          </label>

          {isNew && (
            <label className="block text-sm">
              <span className="mb-1 block font-medium text-slate-700">Product id</span>
              <TextInput
                value={newEntityId}
                required
                placeholder="alp-merino-1"
                onChange={(event) => setNewEntityId(event.target.value)}
              />
            </label>
          )}

          <label className="block text-sm">
            <span className="mb-1 block font-medium text-slate-700">Locale</span>
            <TextInput
              value={locale}
              placeholder="base value"
              onChange={(event) => setLocale(event.target.value.trim())}
            />
            <span className="mt-1 block text-xs text-slate-500">
              A localizable field writes into this locale. Empty writes the base value.
            </span>
          </label>
        </div>

        {form.isPending && <Spinner label="Loading the schema" />}
        {form.isError && <Alert>{form.error.message}</Alert>}

        {form.descriptor !== undefined && (
          <DynamicForm
            descriptor={form.descriptor}
            state={state}
            onChange={setState}
            locale={locale}
            submitting={save.isPending}
            onSubmit={(wire) => save.mutate(wire)}
          />
        )}

        {save.isError && (
          <div className="mt-4">
            <Alert>{save.error instanceof PlatformError ? save.error.message : String(save.error)}</Alert>
          </div>
        )}
        {save.isSuccess && (
          <div className="mt-4">
            <Notice>Saved in one atomic batch. The storefront projects it on the next webhook.</Notice>
          </div>
        )}
      </Card>

      {!isNew && (
        <Card title="Image">
          <input
            type="file"
            accept="image/*"
            aria-label="Product image"
            className="text-sm"
            onChange={(event) => {
              const file = event.target.files?.[0]
              if (file !== undefined) upload.mutate(file)
            }}
          />
          {upload.isPending && <Spinner label="Uploading" />}
          {upload.isError && (
            <Alert>{upload.error instanceof PlatformError ? upload.error.message : String(upload.error)}</Alert>
          )}
          {upload.isSuccess && <Notice>Uploaded. The bytes are in the blob store; the value points at them.</Notice>}
        </Card>
      )}

      {!isNew && (
        <Card title="Withdraw">
          <p className="mb-3 text-sm text-slate-500">
            Archiving removes the product from the storefront on the next event. The values stay readable in
            flexitype.
          </p>
          <SecondaryButton type="button" disabled={archive.isPending} onClick={() => archive.mutate()}>
            {archive.isPending ? 'Archiving…' : 'Archive this product'}
          </SecondaryButton>
          {archive.isError && <Alert>{String(archive.error)}</Alert>}
        </Card>
      )}
    </div>
  )
}

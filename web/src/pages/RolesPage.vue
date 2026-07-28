<script setup lang="ts">
/**
 * Roles: a named permission set many accounts hold.
 *
 * The list endpoints report what is STORED, which is what an operator edits.
 * The effective view reports what the enforcement path computes, which is the
 * only way to answer "is this account safe" without resolving roles by hand —
 * so both are on this page, side by side.
 */
import { computed, ref, watch } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import {
  api,
  friendlyError,
  FIELD_PERMISSIONS,
  type FieldPermission,
  type Role,
  type ServiceAccount,
} from '@/lib/api'
import { accountsHolding, fromPermissionRows, scopesFrom, toPermissionRows } from '@/lib/roles'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import Input from '@/components/ui/Input.vue'
import Drawer from '@/components/ui/Drawer.vue'
import Select from '@/components/ui/Select.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import { Plus, ShieldAlert, Trash2, X } from 'lucide-vue-next'

const toasts = useToasts()
const qc = useQueryClient()

const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => api.listTenants() })
const tenant = ref('')
watch(
  () => tenants.data.value,
  (t) => {
    if (!tenant.value && t?.items.length) tenant.value = t.items[0].name
  },
  { immediate: true },
)

const tenantOptions = computed(() =>
  (tenants.data.value?.items ?? []).map((t) => ({ value: t.name, label: t.name })),
)
const permissionOptions = FIELD_PERMISSIONS.map((l) => ({ value: l, label: l }))

const roles = useQuery({
  queryKey: computed(() => ['roles', tenant.value]),
  queryFn: () => api.listRoles(tenant.value),
  enabled: computed(() => tenant.value !== ''),
})

const accounts = useQuery({
  queryKey: computed(() => ['service-accounts', tenant.value]),
  queryFn: () => api.listServiceAccounts(tenant.value),
  enabled: computed(() => tenant.value !== ''),
})

/** Accounts holding a role, so the delete guard is visible before it fires. */
function holders(name: string): ServiceAccount[] {
  return accountsHolding(accounts.data.value?.items ?? [], name)
}

// --- role editor -------------------------------------------------------------

const editing = ref<Role | null>(null)
const isNew = ref(false)
const form = ref({ name: '', description: '', read: false, write: false })
const perms = ref<Array<{ attribute: string; level: FieldPermission }>>([])

function openNew() {
  isNew.value = true
  editing.value = { id: '', name: '', scopes: [] }
  form.value = { name: '', description: '', read: false, write: false }
  perms.value = []
}

function openEdit(r: Role) {
  isNew.value = false
  editing.value = r
  form.value = {
    name: r.name,
    description: r.description ?? '',
    read: r.scopes.includes('read'),
    write: r.scopes.includes('write'),
  }
  perms.value = toPermissionRows(r.field_permissions)
}

const save = useMutation({
  mutationFn: () => {
    return api.upsertRole({
      tenant_name: tenant.value,
      name: form.value.name.trim(),
      description: form.value.description.trim(),
      scopes: scopesFrom(form.value),
      field_permissions: fromPermissionRows(perms.value),
    })
  },
  onSuccess: (r) => {
    toasts.success(`Saved role ${r.name}`)
    editing.value = null
    void qc.invalidateQueries({ queryKey: ['roles', tenant.value] })
  },
  onError: (e) => toasts.error(friendlyError(e)),
})

const remove = useMutation({
  mutationFn: (name: string) => api.deleteRole(tenant.value, name),
  onSuccess: () => {
    toasts.success('Role deleted')
    void qc.invalidateQueries({ queryKey: ['roles', tenant.value] })
  },
  // A held role answers 409 with the count. Say so rather than "conflict".
  onError: (e) => toasts.error(friendlyError(e)),
})

// --- effective permissions ---------------------------------------------------

const inspecting = ref<ServiceAccount | null>(null)
const effective = useQuery({
  queryKey: computed(() => ['effective-account', inspecting.value?.id]),
  queryFn: () => api.effectiveAccount(inspecting.value!.id),
  enabled: computed(() => inspecting.value !== null),
})
</script>

<template>
  <PageHeader title="Roles" :crumbs="[{ label: 'Roles' }]">
    A role names a permission set once, so many accounts share it instead of each carrying a copy.
    Changes reach every holder as soon as the auth cache entry expires.
  </PageHeader>

  <ErrorState
    v-if="tenants.isError.value"
    :error="tenants.error.value"
    @retry="tenants.refetch()"
  />

  <template v-else>
    <div class="mt-4 flex flex-wrap items-end gap-3">
      <div class="min-w-48">
        <Select v-model="tenant" label="Tenant" :options="tenantOptions" />
      </div>
      <Button class="ml-auto" :disabled="!tenant" @click="openNew()">
        <Plus class="size-4" /> New role
      </Button>
    </div>

    <SkeletonRows v-if="roles.isLoading.value" class="mt-4" :rows="3" />

    <ErrorState
      v-else-if="roles.isError.value"
      :error="roles.error.value"
      @retry="roles.refetch()"
    />

    <EmptyState
      v-else-if="!(roles.data.value?.items ?? []).length"
      class="mt-4"
      title="No roles yet"
      hint="Create one to grant a permission set to many accounts at once."
    />

    <section v-else class="mt-4 overflow-x-auto rounded-lg border border-(--border)">
      <table class="w-full text-sm">
        <thead class="bg-(--surface-muted) text-left text-(--text-muted)">
          <tr>
            <th class="px-3 py-2 font-medium">Role</th>
            <th class="px-3 py-2 font-medium">Scopes</th>
            <th class="px-3 py-2 font-medium">Field permissions</th>
            <th class="px-3 py-2 font-medium">Held by</th>
            <th class="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in roles.data.value?.items ?? []" :key="r.id" class="border-t border-(--border)">
            <td class="px-3 py-2">
              <button class="mono font-medium underline-offset-2 hover:underline" @click="openEdit(r)">
                {{ r.name }}
              </button>
              <p v-if="r.description" class="text-xs text-(--text-muted)">{{ r.description }}</p>
            </td>
            <td class="px-3 py-2">
              <Badge v-for="s in r.scopes" :key="s" class="mr-1">{{ s }}</Badge>
              <span v-if="!r.scopes.length" class="text-(--text-muted)">—</span>
            </td>
            <td class="px-3 py-2">
              <span
                v-for="(level, attr) in r.field_permissions ?? {}"
                :key="attr"
                class="mono mr-2 text-xs"
              >{{ attr }}:{{ level }}</span>
              <span v-if="!Object.keys(r.field_permissions ?? {}).length" class="text-(--text-muted)">—</span>
            </td>
            <td class="px-3 py-2">
              <span v-if="holders(r.name).length" class="mono text-xs">
                {{ holders(r.name).map((a) => a.name).join(', ') }}
              </span>
              <span v-else class="text-(--text-muted)">nobody</span>
            </td>
            <td class="px-3 py-2 text-right">
              <Button
                variant="ghost"
                :disabled="remove.isPending.value"
                :title="
                  holders(r.name).length
                    ? 'Reassign its accounts first: deleting a held role would remove the restrictions it carries'
                    : 'Delete this role'
                "
                @click="remove.mutate(r.name)"
              >
                <Trash2 class="size-4" />
              </Button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <!-- Accounts, with the resolved view a permissions review needs. -->
    <section class="mt-8">
      <h2 class="text-sm font-semibold">Service accounts</h2>
      <p class="mt-1 text-sm text-(--text-muted)">
        The table shows what is stored. Open one to see what it can actually do — its own scopes
        unioned with its roles', and the merged per-attribute levels.
      </p>

      <div class="mt-3 overflow-x-auto rounded-lg border border-(--border)">
        <table class="w-full text-sm">
          <thead class="bg-(--surface-muted) text-left text-(--text-muted)">
            <tr>
              <th class="px-3 py-2 font-medium">Account</th>
              <th class="px-3 py-2 font-medium">Own scopes</th>
              <th class="px-3 py-2 font-medium">Roles</th>
              <th class="px-3 py-2 font-medium">Own overrides</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="a in accounts.data.value?.items ?? []"
              :key="a.id"
              class="cursor-pointer border-t border-(--border) hover:bg-(--surface-muted)"
              @click="inspecting = a"
            >
              <td class="mono px-3 py-2">{{ a.name }}</td>
              <td class="px-3 py-2">
                <Badge v-for="s in a.scopes" :key="s" class="mr-1">{{ s }}</Badge>
                <span v-if="!a.scopes.length" class="text-(--text-muted)">—</span>
              </td>
              <td class="mono px-3 py-2 text-xs">{{ (a.roles ?? []).join(', ') || '—' }}</td>
              <td class="mono px-3 py-2 text-xs">
                {{ Object.entries(a.field_permissions ?? {}).map(([k, v]) => `${k}:${v}`).join(' ') || '—' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Role editor -->
    <Drawer
      :open="editing !== null"
      :title="isNew ? 'New role' : `Edit ${editing?.name}`"
      subtitle="A write replaces the whole role, so what you see here is what it will allow."
      @close="editing = null"
    >
      <form class="space-y-4" @submit.prevent="save.mutate()">
        <label class="block text-sm">
          <span class="mb-1 block text-(--text-muted)">Name</span>
          <Input v-model="form.name" :disabled="!isNew" placeholder="analyst" />
          <span v-if="isNew" class="mt-1 block text-xs text-(--text-muted)">
            Lowercase, 2–64 characters. A write replaces the whole role, so what you see here is
            what it will allow.
          </span>
        </label>

        <label class="block text-sm">
          <span class="mb-1 block text-(--text-muted)">Description</span>
          <Input v-model="form.description" placeholder="reads everything except salaries" />
        </label>

        <fieldset class="text-sm">
          <legend class="mb-1 text-(--text-muted)">Scopes</legend>
          <label class="mr-4 inline-flex items-center gap-2">
            <input v-model="form.read" type="checkbox" /> read
          </label>
          <label class="inline-flex items-center gap-2">
            <input v-model="form.write" type="checkbox" /> write
          </label>
          <p class="mt-1 flex items-start gap-1.5 text-xs text-(--text-muted)">
            <ShieldAlert class="mt-0.5 size-3.5 shrink-0" />
            A role cannot grant <span class="mono">admin</span>: it is a cross-tenant privilege that
            also voids the account's own field permissions, so it is granted on an account where the
            account row shows it.
          </p>
        </fieldset>

        <fieldset class="text-sm">
          <legend class="mb-1 text-(--text-muted)">Field permissions</legend>
          <p class="mb-2 text-xs text-(--text-muted)">
            An attribute that is not listed stays fully accessible. Across roles the most permissive
            level wins; an account's own override beats every role.
          </p>
          <div v-for="(p, i) in perms" :key="i" class="mb-2 flex items-center gap-2">
            <Input v-model="p.attribute" class="flex-1" placeholder="salary" />
            <div class="w-28">
              <Select v-model="p.level" :options="permissionOptions" />
            </div>
            <Button variant="ghost" type="button" @click="perms.splice(i, 1)">
              <X class="size-4" />
            </Button>
          </div>
          <Button variant="ghost" type="button" @click="perms.push({ attribute: '', level: 'none' })">
            <Plus class="size-4" /> Add attribute
          </Button>
        </fieldset>

        <div class="flex justify-end gap-2">
          <Button variant="ghost" type="button" @click="editing = null">Cancel</Button>
          <Button type="submit" :disabled="save.isPending.value || !form.name.trim()">Save</Button>
        </div>
      </form>
    </Drawer>

    <!-- Effective permissions -->
    <Drawer
      :open="inspecting !== null"
      :title="`What ${inspecting?.name} can actually do`"
      subtitle="Resolved by the same code the enforcement path runs."
      @close="inspecting = null"
    >
      <SkeletonRows v-if="effective.isLoading.value" :rows="3" />
      <ErrorState
        v-else-if="effective.isError.value"
        :error="effective.error.value"
        @retry="effective.refetch()"
      />
      <div v-else-if="effective.data.value" class="space-y-4 text-sm">
        <p
          v-if="(effective.data.value.unresolved_roles ?? []).length"
          class="rounded-md border border-(--danger) p-3 text-(--danger)"
        >
          <strong>This account is denied every attribute.</strong>
          It holds
          <span class="mono">{{ effective.data.value.unresolved_roles!.join(', ') }}</span>,
          which no longer exists. A role that resolves to nothing would otherwise read as
          “unrestricted”, so access fails closed until the role is restored or the account is
          reassigned.
        </p>

        <div>
          <h3 class="text-(--text-muted)">Effective scopes</h3>
          <p class="mt-1">
            <Badge v-for="s in effective.data.value.scopes" :key="s" class="mr-1">{{ s }}</Badge>
            <span v-if="!effective.data.value.scopes.length" class="text-(--text-muted)">none</span>
          </p>
        </div>

        <div>
          <h3 class="text-(--text-muted)">Effective field permissions</h3>
          <ul class="mono mt-1 space-y-0.5 text-xs">
            <li v-for="(level, attr) in effective.data.value.field_permissions ?? {}" :key="attr">
              {{ attr }}: {{ level }}
            </li>
          </ul>
          <p
            v-if="!Object.keys(effective.data.value.field_permissions ?? {}).length"
            class="text-(--text-muted)"
          >
            No restrictions — every attribute is readable and writable.
          </p>
        </div>

        <div>
          <h3 class="text-(--text-muted)">Roles held</h3>
          <p class="mono mt-1 text-xs">{{ (effective.data.value.roles ?? []).join(', ') || '—' }}</p>
        </div>
      </div>
    </Drawer>
  </template>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import { buildTypeTree } from '@/lib/tree'
import { formatRelative } from '@/lib/format'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Badge from '@/components/ui/Badge.vue'
import Drawer from '@/components/ui/Drawer.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ErrorState from '@/components/ui/ErrorState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import Pagination from '@/components/ui/Pagination.vue'
import { Plus } from 'lucide-vue-next'

const toasts = useToasts()
const queryClient = useQueryClient()

const cursor = ref<string>()
const includeArchived = ref(false)

const types = useQuery({
  queryKey: ['types', cursor, includeArchived],
  queryFn: () => api.listTypes({ cursor: cursor.value, include_archived: includeArchived.value, limit: 200 }),
})

// The flat page renders as an indented hierarchy; subtypes sit under their
// parents with connector glyphs.
const tree = computed(() => buildTypeTree(types.data.value?.items ?? []))

const drawerOpen = ref(false)
const form = reactive({ internal_name: '', display_name: '', description: '', extends_id: '' })
const formError = ref('')

const extendsOptions = computed(() => [
  { value: '', label: 'No parent (root type)' },
  ...(types.data.value?.items ?? [])
    .filter((t) => !t.archived_at)
    .map((t) => ({ value: t.id, label: t.display_name })),
])

const create = useMutation({
  mutationFn: () =>
    api.createType({
      internal_name: form.internal_name,
      display_name: form.display_name,
      description: form.description || undefined,
      extends_id: form.extends_id || undefined,
    }),
  onSuccess: (t) => {
    queryClient.invalidateQueries({ queryKey: ['types'] })
    toasts.success(`Type "${t.display_name}" created`)
    drawerOpen.value = false
    form.internal_name = ''
    form.display_name = ''
    form.description = ''
    form.extends_id = ''
  },
  onError: (e) => (formError.value = friendlyError(e)),
})
</script>

<template>
  <PageHeader title="Types">
    Soft types are the classes your entities belong to; attributes attach to them.
    <template #actions>
      <label class="flex items-center gap-1.5 text-[13px] text-(--text-muted)">
        <input v-model="includeArchived" type="checkbox" class="accent-(--accent)" />
        Show archived
      </label>
      <Button variant="primary" @click="((drawerOpen = true), (formError = ''))"><Plus :size="15" /> New type</Button>
    </template>
  </PageHeader>

  <div class="overflow-hidden rounded-lg border border-(--border) bg-(--surface)">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-(--border) bg-(--canvas) text-left text-[13px] text-(--text-muted)">
          <th class="px-3 py-2 font-medium">Type</th>
          <th class="px-3 py-2 font-medium">Internal name</th>
          <th class="px-3 py-2 font-medium">Version</th>
          <th class="px-3 py-2 font-medium">Updated</th>
        </tr>
      </thead>
      <tbody>
        <SkeletonRows v-if="types.isPending.value" :rows="5" :cols="4" />
        <tr
          v-for="node in tree"
          v-else
          :key="node.type.id"
          class="border-b border-(--border) last:border-0 hover:bg-(--canvas)"
          :class="{ 'opacity-55': node.type.archived_at }"
        >
          <td class="px-3 py-2.5">
            <span class="flex items-center">
              <span
                v-if="node.depth > 0"
                class="mono text-(--text-muted)"
                :style="{ paddingLeft: `${(node.depth - 1) * 1.25}rem` }"
                aria-hidden="true"
              >{{ node.isLast ? '└─ ' : '├─ ' }}</span>
              <RouterLink :to="`/types/${node.type.id}`" class="font-medium text-(--accent) hover:underline">
                {{ node.type.display_name }}
              </RouterLink>
              <Badge v-if="node.childCount" class="ml-2">{{ node.childCount }} subtype{{ node.childCount > 1 ? 's' : '' }}</Badge>
              <Badge v-if="node.type.archived_at" tone="warn" class="ml-2">archived</Badge>
            </span>
            <p v-if="node.type.description" class="mt-0.5 text-[12.5px] text-(--text-muted)" :style="{ paddingLeft: `${node.depth * 1.25}rem` }">
              {{ node.type.description }}
            </p>
          </td>
          <td class="mono px-3 py-2.5 text-(--text-secondary)">{{ node.type.internal_name }}</td>
          <td class="tnum px-3 py-2.5 text-(--text-secondary)">v{{ node.type.version }}</td>
          <td class="px-3 py-2.5 text-(--text-muted)">{{ formatRelative(node.type.updated_at) }}</td>
        </tr>
      </tbody>
    </table>

    <ErrorState v-if="types.isError.value" :error="types.error.value" class="m-4" @retry="types.refetch()" />

    <EmptyState
      v-else-if="!types.isPending.value && !types.data.value?.items.length"
      title="No types yet"
      body="A type definition is the class an entity belongs to — 'product', 'part', 'ticket'. Create one, then attach attributes."
      class="m-4"
    >
      <template #action>
        <Button variant="primary" @click="drawerOpen = true"><Plus :size="15" /> New type</Button>
      </template>
    </EmptyState>
  </div>

  <Pagination
    :page-info="types.data.value?.page_info"
    :loading="types.isFetching.value"
    @next="(c) => (cursor = c)"
    @reset="cursor = undefined"
  />

  <Drawer :open="drawerOpen" title="New type" @close="drawerOpen = false">
    <form class="flex flex-col gap-4" @submit.prevent="create.mutate()">
      <Input
        v-model="form.internal_name"
        label="Internal name"
        mono
        placeholder="product"
        hint="snake_case; immutable once created"
      />
      <Input v-model="form.display_name" label="Display name" placeholder="Product" />
      <Input v-model="form.description" label="Description" placeholder="Optional" />
      <Select
        v-model="form.extends_id"
        label="Extends"
        :options="extendsOptions"
        hint="A subtype inherits every attribute, constraint and dependency of its parent. Immutable once created."
      />
      <p v-if="formError" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ formError }}</p>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <Button @click="drawerOpen = false">Cancel</Button>
        <Button variant="primary" :disabled="create.isPending.value" @click="create.mutate()">Create type</Button>
      </div>
    </template>
  </Drawer>
</template>

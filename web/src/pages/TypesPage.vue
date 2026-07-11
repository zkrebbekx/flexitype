<script setup lang="ts">
import { reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { api, friendlyError } from '@/lib/api'
import { formatRelative } from '@/lib/format'
import { useToasts } from '@/composables/useToasts'
import PageHeader from '@/components/ui/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Badge from '@/components/ui/Badge.vue'
import Drawer from '@/components/ui/Drawer.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import SkeletonRows from '@/components/ui/SkeletonRows.vue'
import Pagination from '@/components/ui/Pagination.vue'
import { Plus } from 'lucide-vue-next'

const toasts = useToasts()
const queryClient = useQueryClient()

const cursor = ref<string>()
const includeArchived = ref(false)

const types = useQuery({
  queryKey: ['types', cursor, includeArchived],
  queryFn: () => api.listTypes({ cursor: cursor.value, include_archived: includeArchived.value, limit: 25 }),
})

const drawerOpen = ref(false)
const form = reactive({ internal_name: '', display_name: '', description: '' })
const formError = ref('')

const create = useMutation({
  mutationFn: () =>
    api.createType({
      internal_name: form.internal_name,
      display_name: form.display_name,
      description: form.description || undefined,
    }),
  onSuccess: (t) => {
    queryClient.invalidateQueries({ queryKey: ['types'] })
    toasts.success(`Type "${t.display_name}" created`)
    drawerOpen.value = false
    form.internal_name = ''
    form.display_name = ''
    form.description = ''
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
          v-for="t in types.data.value?.items"
          v-else
          :key="t.id"
          class="border-b border-(--border) last:border-0 hover:bg-(--canvas)"
          :class="{ 'opacity-55': t.archived_at }"
        >
          <td class="px-3 py-2.5">
            <RouterLink :to="`/types/${t.id}`" class="font-medium text-(--accent) hover:underline">
              {{ t.display_name }}
            </RouterLink>
            <Badge v-if="t.archived_at" tone="warn" class="ml-2">archived</Badge>
            <p v-if="t.description" class="mt-0.5 text-[12.5px] text-(--text-muted)">{{ t.description }}</p>
          </td>
          <td class="mono px-3 py-2.5 text-(--text-secondary)">{{ t.internal_name }}</td>
          <td class="tnum px-3 py-2.5 text-(--text-secondary)">v{{ t.version }}</td>
          <td class="px-3 py-2.5 text-(--text-muted)">{{ formatRelative(t.updated_at) }}</td>
        </tr>
      </tbody>
    </table>

    <EmptyState
      v-if="!types.isPending.value && !types.data.value?.items.length"
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

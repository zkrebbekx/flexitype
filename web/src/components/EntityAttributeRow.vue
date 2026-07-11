<script setup lang="ts">
import type { AttributeDefinition, AttributeValue } from '@/lib/api'
import { api } from '@/lib/api'
import { renderValue } from '@/lib/format'

// A media value is metadata, not a scalar; narrow it for the preview.
interface MediaMeta {
  object_key: string
  mime: string
  size: number
  filename?: string
}
const asMedia = (v: unknown) => v as MediaMeta
import TypeChip from '@/components/ui/TypeChip.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import RelativeTime from '@/components/ui/RelativeTime.vue'
import { Pencil, Plus, Trash2 } from 'lucide-vue-next'

defineProps<{
  attribute: AttributeDefinition
  // The declaring type (partial — only what effective-attributes returns).
  declaredIn: { id: string; display_name: string }
  values: AttributeValue[]
  ownTypeId: string
}>()
const emit = defineEmits<{
  edit: [attribute: AttributeDefinition, value?: AttributeValue]
  remove: [value: AttributeValue]
}>()
</script>

<template>
  <article class="rounded-lg border border-(--border) bg-(--surface) px-4 py-3">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-2.5">
        <TypeChip :data-type="attribute.data_type" />
        <span class="text-sm font-medium">{{ attribute.display_name }}</span>
        <Badge v-if="attribute.required && !values.length" tone="danger">required, missing</Badge>
        <Badge v-else-if="attribute.required" tone="accent">required</Badge>
        <Badge v-if="attribute.multi_valued">multi</Badge>
        <Badge v-if="attribute.localizable" tone="accent">i18n</Badge>
        <Badge v-if="attribute.scopable" tone="accent">scoped</Badge>
        <Badge v-if="attribute.unique" tone="warn">unique</Badge>
        <span v-if="declaredIn.id !== ownTypeId" class="text-[12px] text-(--text-muted)">
          from {{ declaredIn.display_name }}
        </span>
      </div>
      <Button v-if="!values.length || attribute.multi_valued" size="sm" variant="ghost" @click="emit('edit', attribute)">
        <Plus :size="14" /> {{ values.length ? 'Add value' : 'Set value' }}
      </Button>
    </div>

    <p v-if="attribute.help_text" class="mt-1 text-[13px] text-(--text-muted)">{{ attribute.help_text }}</p>

    <ul v-if="values.length" class="mt-2 flex flex-col gap-1">
      <li
        v-for="v in values"
        :key="v.id"
        class="flex items-center justify-between gap-3 rounded-md bg-(--canvas) px-3 py-1.5"
      >
        <template v-if="attribute.data_type === 'media'">
          <a
            :href="api.mediaUrl(asMedia(v.value).object_key)"
            target="_blank"
            rel="noopener"
            class="flex min-w-0 flex-1 items-center gap-2.5 text-[13px]"
          >
            <img
              v-if="asMedia(v.value).mime?.startsWith('image/')"
              :src="api.mediaUrl(asMedia(v.value).object_key)"
              :alt="asMedia(v.value).filename ?? 'media'"
              class="h-10 w-10 shrink-0 rounded border border-(--border) object-cover"
            />
            <span class="min-w-0 truncate text-(--accent) hover:underline">
              {{ asMedia(v.value).filename || asMedia(v.value).object_key }}
              <span class="text-(--text-muted)">({{ Math.round(asMedia(v.value).size / 1024) }} KB)</span>
            </span>
          </a>
        </template>
        <span v-else class="mono min-w-0 flex-1 truncate text-[13px]">{{ renderValue(v.value) }}</span>
        <span class="flex shrink-0 items-center gap-2 text-[12px] text-(--text-muted)">
          <span :title="`Validated against definition v${v.definition_version}`">def v{{ v.definition_version }}</span>
          <span v-if="v.definition_version < attribute.version" title="The definition has changed since this value was validated">
            <Badge tone="warn">stale</Badge>
          </span>
          <RelativeTime :iso="v.updated_at" />
          <Button size="sm" variant="ghost" aria-label="Edit value" @click="emit('edit', attribute, v)">
            <Pencil :size="13" />
          </Button>
          <Button size="sm" variant="ghost" aria-label="Remove value" @click="emit('remove', v)">
            <Trash2 :size="13" />
          </Button>
        </span>
      </li>
    </ul>
    <p v-else class="mt-1.5 text-[13px] text-(--text-muted)">No value.</p>
  </article>
</template>

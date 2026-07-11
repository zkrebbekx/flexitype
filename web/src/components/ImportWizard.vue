<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, friendlyError } from '@/lib/api'
import type { ImportMapping, ImportReport } from '@/lib/api'
import { useToasts } from '@/composables/useToasts'
import Drawer from '@/components/ui/Drawer.vue'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'

const props = defineProps<{
  open: boolean
  typeId: string
  typeName: string
  attributes: { internal_name: string; display_name: string }[]
}>()
const emit = defineEmits<{ close: []; imported: [] }>()

const toasts = useToasts()

const file = ref<File>()
const columns = ref<string[]>([])
const previewRows = ref<string[][]>([])
const mapping = ref<Record<string, string>>({}) // column -> attribute internal_name ('' = skip)
const keyColumn = ref('')
const mode = ref<'best_effort' | 'transactional'>('best_effort')
const report = ref<ImportReport>()
const busy = ref(false)
const error = ref('')

watch(
  () => props.open,
  (open) => {
    if (!open) reset()
  },
)

function reset() {
  file.value = undefined
  columns.value = []
  previewRows.value = []
  mapping.value = {}
  keyColumn.value = ''
  mode.value = 'best_effort'
  report.value = undefined
  busy.value = false
  error.value = ''
}

// Split one CSV line into fields, honouring double-quoted values. Used only
// to preview the header and a few rows for mapping; the server re-parses the
// whole file authoritatively.
function splitCSVLine(line: string): string[] {
  const out: string[] = []
  let cur = ''
  let inQuotes = false
  for (let i = 0; i < line.length; i++) {
    const c = line[i]
    if (inQuotes) {
      if (c === '"' && line[i + 1] === '"') {
        cur += '"'
        i++
      } else if (c === '"') inQuotes = false
      else cur += c
    } else if (c === '"') inQuotes = true
    else if (c === ',') {
      out.push(cur)
      cur = ''
    } else cur += c
  }
  out.push(cur)
  return out
}

async function onFile(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (!f) return
  file.value = f
  report.value = undefined
  error.value = ''
  const text = await f.text()
  const lines = text.split(/\r?\n/).filter((l) => l.length > 0)
  if (!lines.length) {
    error.value = 'The file is empty.'
    return
  }
  columns.value = splitCSVLine(lines[0])
  previewRows.value = lines.slice(1, 4).map(splitCSVLine)

  // Auto-map columns whose name matches an attribute; pick a sensible key.
  const attrNames = new Set(props.attributes.map((a) => a.internal_name))
  const m: Record<string, string> = {}
  for (const c of columns.value) m[c] = attrNames.has(c) ? c : ''
  mapping.value = m
  keyColumn.value =
    columns.value.find((c) => c === 'entity_id' || c === 'id') ?? columns.value[0] ?? ''
}

const attrOptions = computed(() => [
  { value: '', label: '— skip —' },
  ...props.attributes.map((a) => ({ value: a.internal_name, label: `${a.display_name} (${a.internal_name})` })),
])
const keyOptions = computed(() => columns.value.map((c) => ({ value: c, label: c })))
const modeOptions = [
  { value: 'best_effort', label: 'Best effort — write valid rows, report the rest' },
  { value: 'transactional', label: 'All or nothing — refuse the whole file if any row is invalid' },
]

const mappedCount = computed(() => Object.values(mapping.value).filter(Boolean).length)
const canRun = computed(() => !!file.value && !!keyColumn.value && mappedCount.value > 0 && !busy.value)

function buildMapping(dryRun: boolean): ImportMapping {
  const cols: Record<string, string> = {}
  for (const [col, attr] of Object.entries(mapping.value)) {
    if (attr && col !== keyColumn.value) cols[col] = attr
  }
  return { key_column: keyColumn.value, mapping: cols, mode: mode.value, dry_run: dryRun }
}

async function run(dryRun: boolean) {
  if (!file.value) return
  busy.value = true
  error.value = ''
  try {
    report.value = await api.importEntities(props.typeId, file.value, buildMapping(dryRun))
    if (!dryRun) {
      const r = report.value
      toasts.success(`Imported ${r.rows_written} row${r.rows_written === 1 ? '' : 's'}` + (r.errors.length ? `, ${r.errors.length} skipped` : ''))
      emit('imported')
    }
  } catch (e) {
    error.value = friendlyError(e)
  } finally {
    busy.value = false
  }
}

const shownErrors = computed(() => report.value?.errors.slice(0, 100) ?? [])
</script>

<template>
  <Drawer
    :open="open"
    :title="`Import ${typeName}`"
    subtitle="Upload a CSV, map its columns to attributes, validate, then commit."
    @close="emit('close')"
  >
    <div class="flex flex-col gap-4">
      <div>
        <label class="mb-1 block text-[13px] font-medium text-(--text-secondary)">CSV file</label>
        <input
          type="file"
          accept=".csv,text/csv"
          class="block w-full text-sm text-(--text-secondary) file:mr-3 file:rounded-md file:border file:border-(--border-strong) file:bg-(--canvas) file:px-3 file:py-1.5 file:text-sm file:font-medium"
          @change="onFile"
        />
      </div>

      <template v-if="columns.length">
        <div class="grid grid-cols-2 gap-3">
          <Select v-model="keyColumn" label="Entity id column" :options="keyOptions" />
          <Select v-model="mode" label="Commit mode" :options="modeOptions" />
        </div>

        <div class="rounded-md border border-(--border)">
          <div class="grid grid-cols-2 gap-2 border-b border-(--border) bg-(--canvas) px-3 py-2 text-[12px] font-medium text-(--text-muted)">
            <span>CSV column</span><span>Attribute</span>
          </div>
          <div v-for="col in columns" :key="col" class="grid grid-cols-2 items-center gap-2 px-3 py-1.5">
            <span class="mono truncate text-[13px]" :class="col === keyColumn ? 'text-(--text-muted)' : ''">
              {{ col }}<span v-if="col === keyColumn" class="ml-1 text-[11px]">(key)</span>
            </span>
            <Select v-if="col !== keyColumn" v-model="mapping[col]" :options="attrOptions" />
            <span v-else class="text-[12px] text-(--text-muted)">used as entity id</span>
          </div>
        </div>

        <p v-if="previewRows.length" class="text-[12px] text-(--text-muted)">
          {{ mappedCount }} column{{ mappedCount === 1 ? '' : 's' }} mapped · previewing first {{ previewRows.length }} row{{ previewRows.length === 1 ? '' : 's' }}.
        </p>
      </template>

      <p v-if="error" class="rounded-md bg-(--danger-soft) px-3 py-2 text-[13px] text-(--danger)">{{ error }}</p>

      <div v-if="report" class="rounded-md border border-(--border) p-3">
        <div class="flex flex-wrap gap-x-5 gap-y-1 text-[13px]">
          <span>Total: <b>{{ report.rows_total }}</b></span>
          <span>Valid: <b class="text-(--ok)">{{ report.rows_valid }}</b></span>
          <span v-if="!report.dry_run">Written: <b class="text-(--ok)">{{ report.rows_written }}</b></span>
          <span v-if="report.errors.length">Errors: <b class="text-(--danger)">{{ report.errors.length }}</b></span>
          <span v-if="report.dry_run" class="text-(--text-muted)">(dry run — nothing written)</span>
        </div>
        <div v-if="shownErrors.length" class="mt-2 max-h-64 overflow-y-auto rounded border border-(--border)">
          <table class="w-full text-[12px]">
            <thead class="sticky top-0 bg-(--canvas) text-left text-(--text-muted)">
              <tr><th class="px-2 py-1 font-medium">Row</th><th class="px-2 py-1 font-medium">Column</th><th class="px-2 py-1 font-medium">Reason</th></tr>
            </thead>
            <tbody>
              <tr v-for="(e, idx) in shownErrors" :key="idx" class="border-t border-(--border)">
                <td class="px-2 py-1 tabular-nums">{{ e.row }}</td>
                <td class="mono px-2 py-1">{{ e.column || e.attribute || '—' }}</td>
                <td class="px-2 py-1">{{ e.reason }}</td>
              </tr>
            </tbody>
          </table>
          <p v-if="report.errors.length > shownErrors.length" class="px-2 py-1 text-[11px] text-(--text-muted)">
            +{{ report.errors.length - shownErrors.length }} more…
          </p>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <Button @click="emit('close')">Close</Button>
        <Button :disabled="!canRun" @click="run(true)">Validate (dry run)</Button>
        <Button variant="primary" :disabled="!canRun" @click="run(false)">
          Import
        </Button>
      </div>
    </template>
  </Drawer>
</template>

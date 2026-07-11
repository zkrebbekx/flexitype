<script setup lang="ts">
import { computed, reactive, ref, useId, watch } from 'vue'
import { api, ApiError } from '@/lib/api'
import { suggest } from '@/lib/suggest'
import type { SuggestSchema, Suggestion } from '@/lib/suggest'
import { CircleHelp, Search } from 'lucide-vue-next'

// Stable ids so the combobox can point aria-controls at the listbox and
// aria-activedescendant at the highlighted option (screen-reader support).
const listboxId = useId()
const optionId = (i: number) => `${listboxId}-opt-${i}`

// QueryBar: FQL input with context-aware completions, debounced server
// validation with positioned errors, and a syntax cheat-sheet.
const props = defineProps<{ typeInternalName: string; schema: SuggestSchema }>()
const emit = defineEmits<{ run: [query: string] }>()

const model = defineModel<string>({ default: '' })

const input = ref<HTMLInputElement>()
const dropdown = reactive({ open: false, items: [] as Suggestion[], active: 0, replaceFrom: 0 })
const validation = reactive({ error: '', position: -1 })
const helpOpen = ref(false)

function refreshSuggestions() {
  const el = input.value
  if (!el) return
  const { items, replaceFrom } = suggest(model.value, el.selectionStart ?? model.value.length, props.schema)
  dropdown.items = items
  dropdown.replaceFrom = replaceFrom
  dropdown.active = 0
  dropdown.open = items.length > 0
}

function applySuggestion(s: Suggestion) {
  const el = input.value
  const cursor = el?.selectionStart ?? model.value.length
  const needsSpace = !s.insert.endsWith('(') && !s.insert.endsWith('.')
  const inserted = s.insert + (needsSpace ? ' ' : '')
  model.value = model.value.slice(0, dropdown.replaceFrom) + inserted + model.value.slice(cursor)
  dropdown.open = false
  queueMicrotask(() => {
    el?.focus()
    const pos = dropdown.replaceFrom + inserted.length
    el?.setSelectionRange(pos, pos)
    refreshSuggestions()
  })
}

function onKeydown(e: KeyboardEvent) {
  if (dropdown.open) {
    if (e.key === 'ArrowDown') {
      dropdown.active = (dropdown.active + 1) % dropdown.items.length
      e.preventDefault()
      return
    }
    if (e.key === 'ArrowUp') {
      dropdown.active = (dropdown.active - 1 + dropdown.items.length) % dropdown.items.length
      e.preventDefault()
      return
    }
    // Combobox convention: while the dropdown is open, both Tab and Enter
    // accept the highlighted suggestion. Enter only runs the query when no
    // suggestion is highlighted (dropdown closed) — handled below.
    const highlighted = dropdown.items[dropdown.active]
    if ((e.key === 'Tab' || e.key === 'Enter') && highlighted) {
      applySuggestion(highlighted)
      e.preventDefault()
      return
    }
    if (e.key === 'Escape') {
      dropdown.open = false
      e.preventDefault()
      return
    }
  }
  if (e.key === 'Enter') {
    dropdown.open = false
    emit('run', model.value)
    e.preventDefault()
  }
}

// Debounced server-side validation: parse + bind against the live schema.
let timer: ReturnType<typeof setTimeout> | undefined
watch(model, () => {
  validation.error = ''
  validation.position = -1
  if (timer) clearTimeout(timer)
  if (!model.value.trim()) return
  timer = setTimeout(async () => {
    try {
      await api.validateQuery(props.typeInternalName, model.value)
    } catch (e) {
      if (e instanceof ApiError) {
        validation.error = e.message
        validation.position = typeof e.details?.position === 'number' ? (e.details.position as number) : -1
      }
    }
  }, 350)
})

const errorPointer = computed(() => {
  if (validation.position < 0) return ''
  return ' '.repeat(validation.position) + '↑'
})

const EXAMPLES = [
  `category = "bike" and price >= 500`,
  `icontains(sku, "tb") or "sale" in tags`,
  `count(tags) >= 2 and range(price, 100, 999)`,
  `type isa product and child(supplied_by) { link.lead_time_days <= 14 }`,
]
</script>

<template>
  <div class="relative">
    <div class="relative">
      <Search :size="15" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-(--text-muted)" />
      <input
        ref="input"
        v-model="model"
        type="text"
        role="combobox"
        aria-label="Query entities"
        aria-autocomplete="list"
        :aria-expanded="dropdown.open"
        :aria-controls="listboxId"
        :aria-activedescendant="dropdown.open && dropdown.items[dropdown.active] ? optionId(dropdown.active) : undefined"
        spellcheck="false"
        autocomplete="off"
        placeholder='e.g. category = "bike" and min(price) >= 500 — Tab or Enter completes'
        class="mono h-9 w-full rounded-md border bg-(--surface) pr-9 pl-8 text-[13px]"
        :class="validation.error ? 'border-(--danger)' : 'border-(--border-strong)'"
        @focus="refreshSuggestions"
        @click="refreshSuggestions"
        @input="refreshSuggestions"
        @keydown="onKeydown"
        @blur="dropdown.open = false"
      />
      <button
        class="absolute top-1/2 right-2 -translate-y-1/2 text-(--text-muted) hover:text-(--text)"
        aria-label="Query syntax help"
        @click="helpOpen = !helpOpen"
      >
        <CircleHelp :size="16" />
      </button>
    </div>

    <!-- Positioned error -->
    <div v-if="validation.error" class="mono mt-1 text-[12px] leading-tight text-(--danger)">
      <pre v-if="errorPointer" class="px-8 whitespace-pre">{{ errorPointer }}</pre>
      {{ validation.error }}
    </div>

    <!-- Suggestions -->
    <ul
      v-if="dropdown.open"
      :id="listboxId"
      role="listbox"
      aria-label="Query suggestions"
      class="absolute z-30 mt-1 max-h-72 w-full overflow-y-auto rounded-md border border-(--border) bg-(--surface-raised) py-1 shadow-lg"
    >
      <li
        v-for="(s, i) in dropdown.items"
        :id="optionId(i)"
        :key="s.label"
        role="option"
        :aria-selected="i === dropdown.active"
        class="flex cursor-pointer items-baseline gap-2 px-3 py-1 text-[13px]"
        :class="i === dropdown.active ? 'bg-(--accent-soft)' : ''"
        @mousedown.prevent="applySuggestion(s)"
        @mousemove="dropdown.active = i"
      >
        <span class="mono font-medium">{{ s.label }}</span>
        <span v-if="s.detail" class="truncate text-[12px] text-(--text-muted)">{{ s.detail }}</span>
      </li>
    </ul>

    <!-- Syntax help -->
    <div
      v-if="helpOpen"
      class="absolute right-0 z-30 mt-1 w-[28rem] rounded-md border border-(--border) bg-(--surface-raised) p-3 text-[12.5px] shadow-lg"
    >
      <p class="mb-1.5 font-semibold">Query syntax</p>
      <p class="text-(--text-secondary)">
        Conditions over attributes, joined with <code>and</code>/<code>or</code>/<code>not</code> and parentheses.
        Operators: <code>= != &gt; &gt;= &lt; &lt;= in</code>. Functions: <code>min max count length range has
        contains icontains iequals</code>. Cross relationships with
        <code>child(rel) {…}</code> / <code>parent(rel) {…}</code>, or <code>linked(rel) {…}</code> for
        symmetric links (matches either end); link attributes via <code>link.x</code>.
        <code>type isa x</code> matches a type and its subtypes.
      </p>
      <p class="mt-2 mb-1 font-semibold">Examples</p>
      <ul class="flex flex-col gap-1">
        <li v-for="ex in EXAMPLES" :key="ex">
          <button class="mono text-left text-(--accent) hover:underline" @mousedown.prevent="model = ex; helpOpen = false">
            {{ ex }}
          </button>
        </li>
      </ul>
    </div>
  </div>
</template>

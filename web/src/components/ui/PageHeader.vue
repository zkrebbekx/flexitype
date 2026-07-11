<script setup lang="ts">
import { RouterLink } from 'vue-router'

defineProps<{
  title: string
  crumbs?: { label: string; to?: string }[]
}>()
</script>

<template>
  <header class="mb-5">
    <nav v-if="crumbs?.length" class="mb-1 flex items-center gap-1.5 text-[13px] text-(--text-muted)">
      <template v-for="(c, i) in crumbs" :key="i">
        <RouterLink v-if="c.to" :to="c.to" class="hover:text-(--text-secondary)">{{ c.label }}</RouterLink>
        <span v-else>{{ c.label }}</span>
        <span v-if="i < crumbs.length - 1">/</span>
      </template>
    </nav>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-xl font-semibold tracking-tight">{{ title }}</h1>
      <div v-if="$slots.actions" class="flex items-center gap-2">
        <slot name="actions" />
      </div>
    </div>
    <p v-if="$slots.default" class="mt-1 max-w-2xl text-[13px] text-(--text-muted)"><slot /></p>
  </header>
</template>

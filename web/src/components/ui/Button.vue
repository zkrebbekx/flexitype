<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
    size?: 'sm' | 'md'
    disabled?: boolean
    type?: 'button' | 'submit'
  }>(),
  { variant: 'secondary', size: 'md', disabled: false, type: 'button' },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled"
    class="inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50"
    :class="[
      size === 'sm' ? 'h-7 px-2.5 text-[13px]' : 'h-8.5 px-3.5 text-sm',
      {
        'bg-(--accent) text-white hover:bg-(--accent-hover)': variant === 'primary',
        'border border-(--border-strong) bg-(--surface) text-(--text) hover:border-(--text-muted)':
          variant === 'secondary',
        'text-(--text-secondary) hover:bg-(--accent-soft) hover:text-(--text)': variant === 'ghost',
        'bg-(--danger) text-white hover:bg-(--danger-hover)': variant === 'danger',
      },
    ]"
  >
    <slot />
  </button>
</template>

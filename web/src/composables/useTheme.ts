import { ref, watchEffect } from 'vue'

type Theme = 'light' | 'dark'

const stored = localStorage.getItem('flexitype-theme') as Theme | null
const system: Theme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'

const theme = ref<Theme>(stored ?? system)

watchEffect(() => {
  document.documentElement.classList.toggle('dark', theme.value === 'dark')
  localStorage.setItem('flexitype-theme', theme.value)
})

export function useTheme() {
  const toggle = () => {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
  }
  return { theme, toggle }
}

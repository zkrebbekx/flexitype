import { ref } from 'vue'

export interface Toast {
  id: number
  kind: 'success' | 'error'
  message: string
}

const toasts = ref<Toast[]>([])
let nextId = 1

function push(kind: Toast['kind'], message: string) {
  const id = nextId++
  toasts.value.push({ id, kind, message })
  setTimeout(() => dismiss(id), kind === 'error' ? 6000 : 3000)
}

function dismiss(id: number) {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}

export function useToasts() {
  return {
    toasts,
    dismiss,
    success: (m: string) => push('success', m),
    error: (m: string) => push('error', m),
  }
}

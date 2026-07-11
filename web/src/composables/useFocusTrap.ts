import { onUnmounted, watch, type Ref } from 'vue'

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * useFocusTrap wires a dialog panel for keyboard accessibility while it is
 * open: it saves and restores focus, moves focus into the panel on open,
 * traps Tab / Shift+Tab within it, and closes on Escape.
 *
 * @param open   reactive open state
 * @param panel  ref to the dialog element
 * @param close  called on Escape
 */
export function useFocusTrap(open: Ref<boolean>, panel: Ref<HTMLElement | undefined>, close: () => void) {
  let previousFocus: HTMLElement | null = null

  function focusables(): HTMLElement[] {
    if (!panel.value) return []
    return Array.from(panel.value.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.offsetParent !== null || el === document.activeElement,
    )
  }

  function onKeydown(e: KeyboardEvent) {
    if (!open.value) return
    if (e.key === 'Escape') {
      e.preventDefault()
      close()
      return
    }
    if (e.key !== 'Tab') return
    const items = focusables()
    if (items.length === 0) {
      e.preventDefault()
      return
    }
    const first = items[0]
    const last = items[items.length - 1]
    const active = document.activeElement as HTMLElement
    if (e.shiftKey && (active === first || !panel.value?.contains(active))) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && active === last) {
      e.preventDefault()
      first.focus()
    }
  }

  watch(
    open,
    (isOpen) => {
      if (isOpen) {
        previousFocus = document.activeElement as HTMLElement
        document.addEventListener('keydown', onKeydown)
        // Focus the first focusable once the panel has rendered.
        queueMicrotask(() => focusables()[0]?.focus())
      } else {
        document.removeEventListener('keydown', onKeydown)
        previousFocus?.focus()
        previousFocus = null
      }
    },
    { immediate: true },
  )

  onUnmounted(() => document.removeEventListener('keydown', onKeydown))
}

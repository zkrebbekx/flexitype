import { onBeforeUnmount, watch, type Ref } from 'vue'

/**
 * useDismissable closes a non-modal floating element (popover, menu) when the
 * user presses Escape or interacts outside of it. Listeners are attached only
 * while `open` is true and are cleaned up on close and on unmount.
 *
 * `root` should wrap both the trigger and the floating panel, so clicking the
 * trigger (which toggles `open` itself) is not treated as an outside click.
 *
 * @param open   reactive open state
 * @param root   ref to the element that stays open when interacted with
 * @param close  called to dismiss
 */
export function useDismissable(
  open: Ref<boolean>,
  root: Ref<HTMLElement | undefined>,
  close: () => void,
) {
  function onPointerDown(e: PointerEvent) {
    const target = e.target as Node | null
    if (root.value && target && !root.value.contains(target)) close()
  }
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') close()
  }
  function attach() {
    // Capture phase so we see the interaction before it is stopped elsewhere.
    document.addEventListener('pointerdown', onPointerDown, true)
    document.addEventListener('keydown', onKeydown)
  }
  function detach() {
    document.removeEventListener('pointerdown', onPointerDown, true)
    document.removeEventListener('keydown', onKeydown)
  }

  watch(open, (isOpen) => (isOpen ? attach() : detach()))
  onBeforeUnmount(detach)
}

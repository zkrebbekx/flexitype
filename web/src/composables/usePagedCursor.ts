import { computed, ref } from 'vue'

/**
 * usePagedCursor tracks forward/backward paging over the API's opaque list
 * cursors. Cursors can't be decoded client-side, so we keep a history of
 * the cursors that loaded prior pages and pop it to page back.
 */
export function usePagedCursor() {
  // The cursor that loaded the currently displayed page (undefined = first).
  const cursor = ref<string>()
  // Cursors of the pages before the current one.
  const history = ref<string[]>([])

  return {
    cursor,
    canPrevious: computed(() => history.value.length > 0),
    next(nextCursor: string) {
      history.value.push(cursor.value ?? '')
      cursor.value = nextCursor
    },
    previous() {
      cursor.value = history.value.pop() || undefined
    },
    reset() {
      history.value = []
      cursor.value = undefined
    },
  }
}

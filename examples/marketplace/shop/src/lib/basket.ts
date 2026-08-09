import { useCallback, useEffect, useState } from 'react'

/** One line of the basket. */
export interface BasketLine {
  entityId: string
  name: string
  merchant: string
  price?: string
  currency: string
  quantity: number
}

const STORAGE_KEY = 'marketplace.basket'
const CHANGED = 'marketplace.basket.changed'

function read(): BasketLine[] {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (raw === null) return []
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as BasketLine[]) : []
  } catch {
    return []
  }
}

function write(lines: BasketLine[]): void {
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(lines))
  window.dispatchEvent(new Event(CHANGED))
}

function keyOf(line: Pick<BasketLine, 'entityId'>): string {
  return line.entityId
}

/**
 * The basket, in local storage.
 *
 * It is a browser-side list, not an order: the example stops where checkout
 * would begin. One storefront is one merchant, so a basket is one merchant's
 * order and an entity id identifies a line on its own.
 */
export function useBasket() {
  const [lines, setLines] = useState<BasketLine[]>(read)

  useEffect(() => {
    const refresh = () => setLines(read())
    window.addEventListener(CHANGED, refresh)
    window.addEventListener('storage', refresh)
    return () => {
      window.removeEventListener(CHANGED, refresh)
      window.removeEventListener('storage', refresh)
    }
  }, [])

  const add = useCallback((line: Omit<BasketLine, 'quantity'>) => {
    const current = read()
    const existing = current.find((entry) => keyOf(entry) === keyOf(line))
    if (existing === undefined) {
      write([...current, { ...line, quantity: 1 }])
      return
    }
    write(
      current.map((entry) =>
        keyOf(entry) === keyOf(line) ? { ...entry, quantity: entry.quantity + 1 } : entry,
      ),
    )
  }, [])

  const remove = useCallback((line: Pick<BasketLine, 'entityId'>) => {
    write(read().filter((entry) => keyOf(entry) !== keyOf(line)))
  }, [])

  const clear = useCallback(() => {
    write([])
  }, [])

  const count = lines.reduce((total, line) => total + line.quantity, 0)

  return { lines, add, remove, clear, count }
}

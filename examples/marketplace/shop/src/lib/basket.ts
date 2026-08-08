import { useCallback, useEffect, useState } from 'react'

/** One line of the basket. A product is identified by its tenant AND its id. */
export interface BasketLine {
  tenant: string
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

function keyOf(line: Pick<BasketLine, 'tenant' | 'entityId'>): string {
  return `${line.tenant} ${line.entityId}`
}

/**
 * The basket, in local storage.
 *
 * It is a browser-side list, not an order. The example stops where a real
 * marketplace would start splitting one basket into one order per merchant,
 * because each merchant is a separate tenant and a separate settlement.
 *
 * A line is keyed by tenant AND entity id. Two merchants can both call a
 * product `sku-1`, and keying on the id alone would merge them into one line.
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

  const remove = useCallback((line: Pick<BasketLine, 'tenant' | 'entityId'>) => {
    write(read().filter((entry) => keyOf(entry) !== keyOf(line)))
  }, [])

  const clear = useCallback(() => {
    write([])
  }, [])

  const count = lines.reduce((total, line) => total + line.quantity, 0)

  return { lines, add, remove, clear, count }
}

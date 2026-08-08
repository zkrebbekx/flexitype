import '@testing-library/jest-dom/vitest'

// happy-dom does not implement Web Storage, so `window.localStorage` is
// undefined under the test environment while it is always present in a
// browser. The basket is deliberately stored there, so the tests supply the
// minimum implementation the specification requires rather than the basket
// carrying a fallback no browser would ever take.
if (typeof window !== 'undefined' && window.localStorage === undefined) {
  const entries = new Map<string, string>()
  const storage: Storage = {
    get length() {
      return entries.size
    },
    clear: () => entries.clear(),
    getItem: (key) => entries.get(String(key)) ?? null,
    key: (index) => [...entries.keys()][index] ?? null,
    removeItem: (key) => {
      entries.delete(String(key))
    },
    setItem: (key, value) => {
      entries.set(String(key), String(value))
    },
  }
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true })
}

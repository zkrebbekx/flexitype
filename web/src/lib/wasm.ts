// Playground boot: loads the flexitype service compiled to WebAssembly and
// installs a fetch shim so the console's API calls run in-browser instead
// of over the network. Only imported when the build sets VITE_WASM=1.

interface WasmResponse {
  status: number
  body: string
  contentType: string
}

interface GoRuntime {
  importObject: WebAssembly.Imports
  run(instance: WebAssembly.Instance): Promise<void>
}

declare global {
  interface Window {
    Go: new () => GoRuntime
    __flexitypeFetch?: (method: string, path: string, body?: string) => Promise<WasmResponse>
    __flexitypeReady?: boolean
    __flexitypeOnReady?: () => void
  }
}

// Statuses the Response constructor refuses a body for.
const BODYLESS = new Set([101, 204, 205, 304])

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const el = document.createElement('script')
    el.src = src
    el.onload = () => resolve()
    el.onerror = () => reject(new Error(`failed to load ${src}`))
    document.head.appendChild(el)
  })
}

function installFetchShim(): void {
  const realFetch = window.fetch.bind(window)
  window.fetch = async (input, init) => {
    const url =
      typeof input === 'string' ? input : input instanceof URL ? input.pathname : input.url
    if (url.startsWith('/api/') || url === '/healthz' || url === '/readyz') {
      const method = init?.method ?? 'GET'
      const body = typeof init?.body === 'string' ? init.body : undefined
      const res = await window.__flexitypeFetch!(method, url, body)
      return new Response(BODYLESS.has(res.status) ? null : res.body, {
        status: res.status,
        headers: { 'Content-Type': res.contentType || 'application/json' },
      })
    }
    return realFetch(input, init)
  }
}

// bootWasm loads wasm_exec.js and the service binary, waits for the Go
// side to publish its bridge, then reroutes API fetches through it.
export async function bootWasm(): Promise<void> {
  const base = import.meta.env.BASE_URL
  await loadScript(`${base}wasm_exec.js`)

  const go = new window.Go()
  const result = await WebAssembly.instantiateStreaming(
    fetch(`${base}flexitype.wasm`),
    go.importObject,
  )

  const ready = new Promise<void>((resolve) => {
    if (window.__flexitypeReady) {
      resolve()
      return
    }
    window.__flexitypeOnReady = resolve
  })
  void go.run(result.instance) // resolves only when the service exits
  await ready

  installFetchShim()
}

// Console authentication: the standalone service protects /api/v1 with a
// service-account bearer token. The console has no token of its own, so it
// holds one the operator pastes in, persisted for the browser session, and
// attaches it to every request. In development / playground mode the service
// runs with auth disabled, so no token is ever needed and the sign-in overlay
// never appears (nothing returns 401).
import { computed, ref } from 'vue'

const STORAGE_KEY = 'flexitype.token'

function load(): string {
  try {
    return sessionStorage.getItem(STORAGE_KEY) ?? ''
  } catch {
    return '' // storage may be unavailable (sandboxed embed)
  }
}

/** The current bearer token (empty when none set). */
export const token = ref(load())

/** True once the server has rejected a request for missing/invalid auth. */
export const authRequired = ref(false)

/** Whether a token is currently held. */
export const isAuthenticated = computed(() => token.value !== '')

/** Store (or clear) the token for the browser session. */
export function setToken(t: string) {
  token.value = t.trim()
  try {
    if (token.value) sessionStorage.setItem(STORAGE_KEY, token.value)
    else sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // ignore — token still held in memory for this session
  }
  authRequired.value = false
}

/** Forget the token. */
export function signOut() {
  setToken('')
}

/**
 * Mark that the server rejected a request for lack of valid credentials, so
 * the console shows the sign-in overlay.
 */
export function challenge() {
  authRequired.value = true
}

/** Authorization header for the current token, or an empty object. */
export function bearerHeader(): Record<string, string> {
  return token.value ? { Authorization: `Bearer ${token.value}` } : {}
}

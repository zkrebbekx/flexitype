import { createClient, type FlexitypeClient } from '@flexitype/client'

/**
 * The flexitype client for one merchant.
 *
 * Its base URL is the platform's read-only passthrough, which attaches that
 * merchant's service-account token server-side. The browser therefore speaks
 * the REAL flexitype API — every SDK service, hook and soft-typing helper
 * works unchanged — while holding no merchant credential of its own.
 *
 * A write through this client is refused with 405 by design. Write through
 * `platform.ts`, which batches a whole product atomically.
 */
const clients = new Map<string, FlexitypeClient>()

export function merchantClient(merchantId: string): FlexitypeClient {
  const existing = clients.get(merchantId)
  if (existing !== undefined) return existing
  const client = createClient({
    baseUrl: `/api/merchants/${encodeURIComponent(merchantId)}/flexitype`,
    userAgent: 'marketplace-console',
  })
  clients.set(merchantId, client)
  return client
}

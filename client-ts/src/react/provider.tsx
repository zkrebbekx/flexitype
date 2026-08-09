import { createContext, createElement, useContext, type ReactNode } from 'react'
import type { FlexitypeClient } from '../client.js'
import { FlexitypeError } from '../errors.js'

const ClientContext = createContext<FlexitypeClient | undefined>(undefined)

/** The props `FlexitypeProvider` takes. */
export interface FlexitypeProviderProps {
  /** The client the hooks below use. One client is one tenant. */
  client: FlexitypeClient
  children?: ReactNode
}

/**
 * Supplies a client to the hooks under it.
 *
 * It does NOT create a `QueryClientProvider`. TanStack Query's cache belongs to
 * the application, which usually configures retries, staleness and
 * dev-tools on it; nesting a second one here would split the cache in two and
 * make an invalidation from application code miss these hooks' entries.
 *
 * ```tsx
 * <QueryClientProvider client={queryClient}>
 *   <FlexitypeProvider client={flexitype}>
 *     <App />
 *   </FlexitypeProvider>
 * </QueryClientProvider>
 * ```
 *
 * An application that talks to several tenants nests one provider per tenant,
 * or swaps the `client` prop, because the tenant travels in the token.
 *
 * A shared cache is safe across that swap because every key names the client
 * (`client.cacheKey`), so one tenant's entries cannot be read under another.
 * Two clients that must not share cached data must not share a `cacheKey`:
 * it is derived from the base URL and a hash of the token, so give them
 * distinct `cacheKeyPrefix` values if they would otherwise be identical.
 */
export function FlexitypeProvider({ client, children }: FlexitypeProviderProps) {
  return createElement(ClientContext.Provider, { value: client }, children)
}

/**
 * The client from the nearest provider.
 *
 * It raises rather than returning undefined: a hook that silently did nothing
 * without a provider would look like an empty result set.
 */
export function useFlexitypeClient(): FlexitypeClient {
  const client = useContext(ClientContext)
  if (client === undefined) {
    throw new FlexitypeError({
      code: 'INTERNAL',
      message: 'flexitype: no client in context; wrap the tree in <FlexitypeProvider client={...}>',
      status: 0,
    })
  }
  return client
}

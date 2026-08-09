import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'

import App from './App.js'
import './index.css'

// One cache for the whole console. The SDK's FlexitypeProvider deliberately
// does not create its own, so an invalidation from application code reaches
// the hooks' entries.
//
// One cache across several merchants is safe because every hook key names the
// client it came from. Each merchant gets its own client (see
// lib/merchantClient.ts), whose cacheKey differs — so switching merchants
// cannot serve one merchant's types or products under another, which a
// tenant-agnostic key silently would.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 5_000, refetchOnWindowFocus: false },
  },
})

const root = document.getElementById('root')
if (root === null) throw new Error('no #root element')

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)

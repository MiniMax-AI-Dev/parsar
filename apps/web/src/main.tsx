import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { App } from './App'
import { bootstrapWorkspace } from './lib/bootstrap'
import { prefetchProviderCatalog } from './lib/model-presets'
import './style.css'
import './i18n' // bootstrap i18next
import './i18n/types' // type-augment t() keys

// Defaults tuned for an admin UI: don't refetch aggressively on focus, keep
// cache around for snappy back/forward navigation.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      staleTime: 30_000,
      gcTime: 5 * 60_000,
    },
  },
})

// Best-effort; never blocks first render.
void bootstrapWorkspace()
prefetchProviderCatalog()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)

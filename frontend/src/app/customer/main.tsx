import '@/styles/index.css'

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router'

import { AppProviders } from '@/app/providers'

import { router } from './router'

// Customer entry (/menu…). Plain web app: no manifest, no service worker (SECURITY §11).
// Must never import from src/app/admin or src/features/admin (enforced by ESLint).
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>
  </StrictMode>,
)

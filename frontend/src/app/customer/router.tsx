import { createBrowserRouter, Navigate } from 'react-router'

import { RouteError } from '@/app/RouteError'
import { NotFoundPage } from '@/components/NotFoundPage'
import { CustomerLayout } from '@/features/menu/CustomerLayout'
import { MenuPage } from '@/features/menu/MenuPage'

export const router = createBrowserRouter([
  { path: '/', element: <Navigate to="/menu" replace /> },
  {
    path: '/menu',
    element: <CustomerLayout />,
    errorElement: <RouteError />,
    children: [
      { index: true, element: <MenuPage /> },
      // T10: cart, checkout and /menu/order (tracking token in the URL fragment).
    ],
  },
  { path: '*', element: <NotFoundPage homeHref="/menu" /> },
])

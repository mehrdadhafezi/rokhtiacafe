import { createBrowserRouter } from 'react-router'

import { RouteError } from '@/app/RouteError'
import { NotFoundPage } from '@/components/NotFoundPage'
import { AdminLayout } from '@/features/admin/AdminLayout'
import { DashboardPage } from '@/features/admin/dashboard/DashboardPage'

export const router = createBrowserRouter(
  [
    {
      path: '/',
      element: <AdminLayout />,
      errorElement: <RouteError />,
      children: [
        { index: true, element: <DashboardPage /> },
        // T11: login, orders, payments, catalog, inventory, purchases, expenses, reports, users.
        { path: '*', element: <NotFoundPage homeHref="/admin/" /> },
      ],
    },
  ],
  { basename: '/admin' },
)

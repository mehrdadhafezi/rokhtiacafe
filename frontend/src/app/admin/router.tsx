import type { ReactNode } from 'react'
import { createBrowserRouter } from 'react-router'

import { RouteError } from '@/app/RouteError'
import { NotFoundPage } from '@/components/NotFoundPage'
import { AccountPage } from '@/features/admin/account/AccountPage'
import { AdminLayout } from '@/features/admin/AdminLayout'
import { RequirePermission } from '@/features/admin/auth'
import { CatalogPage } from '@/features/admin/catalog/CatalogPage'
import { DashboardPage } from '@/features/admin/dashboard/DashboardPage'
import { ExpensesPage } from '@/features/admin/expenses/ExpensesPage'
import { InventoryPage } from '@/features/admin/inventory/InventoryPage'
import { OrderDetailPage } from '@/features/admin/orders/OrderDetailPage'
import { OrdersPage } from '@/features/admin/orders/OrdersPage'
import type { Action } from '@/features/admin/permissions'
import { PurchasesPage } from '@/features/admin/purchases/PurchasesPage'
import { ReportsPage } from '@/features/admin/reports/ReportsPage'
import { UsersPage } from '@/features/admin/users/UsersPage'

const guard = (action: Action, element: ReactNode) => <RequirePermission action={action}>{element}</RequirePermission>

export const router = createBrowserRouter(
  [
    {
      path: '/',
      element: <AdminLayout />,
      errorElement: <RouteError />,
      children: [
        { index: true, element: guard('dashboard.view', <DashboardPage />) },
        { path: 'orders', element: guard('orders.view', <OrdersPage />) },
        { path: 'orders/:id', element: guard('orders.view', <OrderDetailPage />) },
        { path: 'catalog', element: guard('catalog.view', <CatalogPage />) },
        { path: 'inventory', element: guard('inventory.view_qty', <InventoryPage />) },
        { path: 'purchases', element: guard('inventory.manage', <PurchasesPage />) },
        { path: 'expenses', element: guard('expenses.manage', <ExpensesPage />) },
        { path: 'reports', element: guard('reports.view', <ReportsPage />) },
        { path: 'users', element: guard('users.manage', <UsersPage />) },
        { path: 'account', element: <AccountPage /> },
        { path: '*', element: <NotFoundPage homeHref="/admin/" /> },
      ],
    },
  ],
  { basename: '/admin' },
)

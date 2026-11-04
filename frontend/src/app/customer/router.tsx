import { createBrowserRouter, Navigate } from 'react-router'

import { RouteError } from '@/app/RouteError'
import { NotFoundPage } from '@/components/NotFoundPage'
import { AccountPage, MyOrderPage, MyOrdersPage } from '@/features/account/AccountPages'
import { LoginPage, RegisterPage, RequireCustomer } from '@/features/account/AuthPages'
import { CartPage } from '@/features/cart/CartPage'
import { CustomerLayout } from '@/features/menu/CustomerLayout'
import { MenuPage } from '@/features/menu/MenuPage'
import { OrderPage } from '@/features/order/OrderPage'

export const router = createBrowserRouter([
  { path: '/', element: <Navigate to="/menu" replace /> },
  {
    path: '/menu',
    element: <CustomerLayout />,
    errorElement: <RouteError />,
    children: [
      { index: true, element: <MenuPage /> },
      { path: 'cart', element: <CartPage /> },
      // The tracking token lives in the URL fragment (/menu/order#<token>) so it never reaches
      // the server in a URL, logs or Referer (SECURITY §10).
      { path: 'order', element: <OrderPage /> },
      // Customer accounts (optional: guests keep ordering without one).
      { path: 'login', element: <LoginPage /> },
      { path: 'register', element: <RegisterPage /> },
      { path: 'account', element: <RequireCustomer><AccountPage /></RequireCustomer> },
      { path: 'my-orders', element: <RequireCustomer><MyOrdersPage /></RequireCustomer> },
      { path: 'my-orders/:id', element: <RequireCustomer><MyOrderPage /></RequireCustomer> },
    ],
  },
  { path: '*', element: <NotFoundPage homeHref="/menu" /> },
])

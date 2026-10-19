import { createBrowserRouter, Navigate } from 'react-router'

import { RouteError } from '@/app/RouteError'
import { NotFoundPage } from '@/components/NotFoundPage'
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
    ],
  },
  { path: '*', element: <NotFoundPage homeHref="/menu" /> },
])

import { Link, Outlet, useLocation } from 'react-router'

import { BagIcon, CupIcon } from '@/components/icons'
import { CartProvider, useCart } from '@/features/cart/CartContext'
import { cartCount } from '@/features/cart/cart'
import { faDigits } from '@/lib/digits'

function HeaderCart() {
  const { cart } = useCart()
  const { pathname } = useLocation()
  const count = cartCount(cart)
  if (pathname.startsWith('/menu/cart')) return null
  return (
    <Link
      to="/menu/cart"
      className="relative grid size-11 place-items-center rounded-full text-primary transition hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50"
      aria-label={count ? `سبد خرید، ${faDigits(count)} قلم` : 'سبد خرید خالی'}
    >
      <BagIcon width={24} height={24} />
      {count > 0 && (
        <span className="absolute -top-0.5 -end-0.5 grid min-w-5 place-items-center rounded-full bg-accent px-1 text-xs font-bold text-white tabular-nums">
          {faDigits(count)}
        </span>
      )}
    </Link>
  )
}

/** Customer shell: brand header with cart shortcut; the cart state lives here for all /menu pages. */
export function CustomerLayout() {
  return (
    <CartProvider>
      <div className="mx-auto flex min-h-dvh max-w-lg flex-col">
        <header className="sticky top-0 z-20 border-b border-border/70 bg-background/90 px-4 pt-[env(safe-area-inset-top)] backdrop-blur-md">
          <div className="flex h-14 items-center justify-between">
            <Link to="/menu" className="flex items-center gap-2 rounded-md focus-visible:ring-[3px] focus-visible:ring-ring/50">
              <span className="grid size-9 place-items-center rounded-full bg-primary text-primary-foreground">
                <CupIcon width={20} height={20} />
              </span>
              <span className="leading-tight">
                <span className="block text-lg font-extrabold text-primary">رخ تیا</span>
                <span className="block text-xs text-muted-foreground">کافه</span>
              </span>
            </Link>
            <HeaderCart />
          </div>
        </header>
        <main className="flex-1 px-4 pt-4 pb-32">
          <Outlet />
        </main>
      </div>
    </CartProvider>
  )
}

import { NavLink, Outlet } from 'react-router'

import { CupIcon } from '@/components/icons'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { AuthProvider } from './auth'
import { useAuth } from './hooks'
import { roleLabel } from './labels'
import type { Action } from './permissions'
import { ToastProvider } from './ui'

const nav: { to: string; label: string; action: Action; end?: boolean }[] = [
  { to: '/', label: 'داشبورد', action: 'dashboard.view', end: true },
  { to: '/orders', label: 'سفارش‌ها', action: 'orders.view' },
  { to: '/catalog', label: 'منو و محصولات', action: 'catalog.view' },
  { to: '/inventory', label: 'موجودی', action: 'inventory.view_qty' },
  { to: '/purchases', label: 'خریدها', action: 'inventory.manage' },
  { to: '/expenses', label: 'هزینه‌ها', action: 'expenses.manage' },
  { to: '/reports', label: 'گزارش‌ها', action: 'reports.view' },
  { to: '/users', label: 'کاربران', action: 'users.manage' },
]

function Shell() {
  const { user, can, logout } = useAuth()
  const items = nav.filter((n) => can(n.action))

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="sticky top-0 z-30 border-b border-border/70 bg-background/95 pt-[env(safe-area-inset-top)] backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-7xl items-center justify-between gap-3 px-4">
          <div className="flex items-center gap-2">
            <span className="grid size-9 place-items-center rounded-full bg-primary text-primary-foreground" aria-hidden>
              <CupIcon width={20} height={20} />
            </span>
            <div className="leading-tight">
              <p className="font-extrabold">رخ تیا</p>
              <p className="text-xs text-muted-foreground">پنل کارکنان</p>
            </div>
          </div>
          <div className="flex items-center gap-1">
            <NavLink
              to="/account"
              className="flex h-11 items-center gap-2 rounded-lg px-2 text-sm hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50"
              aria-label={`حساب کاربری ${user.display_name}`}
            >
              <span className="hidden font-medium sm:inline">{user.display_name}</span>
              <span className="rounded-full bg-muted px-2 py-0.5 text-xs">{roleLabel[user.role]}</span>
            </NavLink>
            <Button variant="ghost" onClick={logout}>
              خروج
            </Button>
          </div>
        </div>
        {/* Phones and tablets: a scrollable row of sections. */}
        <nav aria-label="بخش‌ها" className="mx-auto flex max-w-7xl gap-2 overflow-x-auto px-4 pb-2 lg:hidden">
          {items.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              className={({ isActive }) =>
                cn(
                  'flex h-11 shrink-0 items-center rounded-full border px-4 text-sm font-medium whitespace-nowrap transition focus-visible:ring-[3px] focus-visible:ring-ring/50',
                  isActive ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-surface hover:bg-muted',
                )
              }
            >
              {n.label}
            </NavLink>
          ))}
        </nav>
      </header>

      <div className="mx-auto flex w-full max-w-7xl flex-1 gap-6 px-4 py-4 lg:py-6">
        <nav aria-label="بخش‌ها" className="hidden w-52 shrink-0 lg:block">
          <ul className="sticky top-20 space-y-1">
            {items.map((n) => (
              <li key={n.to}>
                <NavLink
                  to={n.to}
                  end={n.end}
                  className={({ isActive }) =>
                    cn(
                      'flex h-11 items-center rounded-lg px-3 font-medium transition focus-visible:ring-[3px] focus-visible:ring-ring/50',
                      isActive ? 'bg-primary text-primary-foreground' : 'hover:bg-muted',
                    )
                  }
                >
                  {n.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <main className="min-w-0 flex-1 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function AdminLayout() {
  return (
    <ToastProvider>
      <AuthProvider>
        <Shell />
      </AuthProvider>
    </ToastProvider>
  )
}

import { Outlet } from 'react-router'

export function AdminLayout() {
  return (
    <div className="flex min-h-dvh flex-col">
      <header className="sticky top-0 z-10 bg-primary px-4 py-3 text-primary-foreground">
        <h1 className="text-lg font-bold">رخ تیا — پنل کارکنان</h1>
      </header>
      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-4">
        <Outlet />
      </main>
    </div>
  )
}

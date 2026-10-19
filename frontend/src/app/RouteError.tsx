import { useRouteError } from 'react-router'

import { ErrorState } from '@/components/states'

/** Router error element: render errors and loader failures end up here instead of a blank page. */
export function RouteError() {
  const error = useRouteError()
  return (
    <main className="mx-auto max-w-lg p-4">
      <ErrorState error={error} onRetry={() => window.location.reload()} />
    </main>
  )
}

import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo, type ReactNode } from 'react'

import { customerKey, meKey, type CustomerSession } from './api'
import { loadSession, SessionContext, sessionState, type SessionValue } from './session-context'

/** Provides the customer session to every /menu page (see session-context.ts). */
export function CustomerSessionProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  // A failed lookup (network) leaves the visitor a guest, so the menu and guest ordering work.
  const me = useQuery({ queryKey: meKey, queryFn: ({ signal }) => loadSession(signal), staleTime: 60_000, retry: 1 })
  const pending = me.isPending
  const data = me.data

  const signedIn = useCallback(
    (s: CustomerSession) => {
      queryClient.removeQueries({ queryKey: customerKey, predicate: (q) => q.queryKey[1] !== 'me' })
      queryClient.setQueryData(meKey, s)
    },
    [queryClient],
  )
  const signedOut = useCallback(() => {
    queryClient.removeQueries({ queryKey: customerKey, predicate: (q) => q.queryKey[1] !== 'me' })
    queryClient.setQueryData(meKey, null)
  }, [queryClient])

  const value = useMemo<SessionValue>(() => {
    const state = sessionState(data, pending && !me.isError)
    return { state, csrf: state.status === 'customer' ? state.csrf : null, signedIn, signedOut }
  }, [data, pending, me.isError, signedIn, signedOut])
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

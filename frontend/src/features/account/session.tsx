import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo, type ReactNode } from 'react'

import { customerKey, meKey, type CustomerSession } from './api'
import { SessionContext, sessionQueryOptions, sessionState, type SessionValue } from './session-context'

/** Provides the customer session to every /menu page (see session-context.ts). */
export function CustomerSessionProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  // A 401 is a guest; any other failure is an unknown identity ("error"), shown with a retry.
  const me = useQuery(sessionQueryOptions())
  const { data, isError, isFetching, refetch } = me

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
  const retry = useCallback(() => void refetch(), [refetch])

  const value = useMemo<SessionValue>(() => {
    const state = sessionState({ data, isError })
    return { state, csrf: state.status === 'customer' ? state.csrf : null, checking: isFetching, retry, signedIn, signedOut }
  }, [data, isError, isFetching, retry, signedIn, signedOut])
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

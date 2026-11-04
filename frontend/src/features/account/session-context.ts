import { createContext, useContext } from 'react'

import { isApiError } from '@/lib/api'

import { customerApi, meKey, type Customer, type CustomerSession } from './api'

/**
 * The customer's login state for the /menu app. The session cookie is HttpOnly; the CSRF token
 * lives only in memory (the React Query cache), never in storage, URLs or logs. Guests keep
 * ordering exactly as before; nothing here touches the staff session.
 *
 * "guest" is only ever the answer of the server (401 from /customer/auth/me, or a logout). When the
 * lookup fails for any other reason (network, 5xx) the identity is unknown: the browser may still
 * hold a valid customer cookie, and an order sent without the customer CSRF token would be refused,
 * so the state is "error" — never a silent guest — until a retry succeeds.
 */

export type SessionState =
  | { status: 'loading' }
  | { status: 'guest' }
  | { status: 'customer'; customer: Customer; csrf: string }
  | { status: 'error' }

export interface SessionValue {
  state: SessionState
  /** The customer CSRF token, or null for guests (also null while loading or unknown). */
  csrf: string | null
  /** A session lookup is in flight (first load or a retry). */
  checking: boolean
  /** Ask the server again (the "try again" button of the error state). */
  retry: () => void
  /** Store a session returned by login, registration or a password change. */
  signedIn: (s: CustomerSession) => void
  /** Forget the session locally (after logout, or when the server reports it expired). */
  signedOut: () => void
}

export const SessionContext = createContext<SessionValue | null>(null)

export function useCustomerSession(): SessionValue {
  const v = useContext(SessionContext)
  if (!v) throw new Error('useCustomerSession outside CustomerSessionProvider')
  return v
}

/** GET /customer/auth/me; a 401 means "guest", not an error. Anything else that fails is thrown. */
export async function loadSession(signal?: AbortSignal): Promise<CustomerSession | null> {
  try {
    return await customerApi.me(signal)
  } catch (err) {
    if (isApiError(err) && err.status === 401) return null
    throw err
  }
}

/** Automatic retries of a failed lookup before the error state is shown (401 is never retried: it is an answer). */
export const SESSION_RETRIES = 2

/** The /me query, shared by the provider and its tests. */
export function sessionQueryOptions(retryDelay?: number) {
  return {
    queryKey: meKey,
    queryFn: ({ signal }: { signal?: AbortSignal }) => loadSession(signal),
    staleTime: 60_000,
    retry: SESSION_RETRIES,
    ...(retryDelay === undefined ? {} : { retryDelay }),
  }
}

/**
 * Maps the /me query to a state. A known answer wins (a failed background refresh keeps the last
 * answer: an expired session is then reported by the server on the next customer request); with no
 * answer yet, a failure is "error", never "guest".
 */
export function sessionState(q: { data: CustomerSession | null | undefined; isError: boolean }): SessionState {
  if (q.data === null) return { status: 'guest' }
  if (q.data) return { status: 'customer', customer: q.data.customer, csrf: q.data.csrf_token }
  if (q.isError) return { status: 'error' }
  return { status: 'loading' }
}

/**
 * The identity an order is placed under: the customer token, null for a guest, or "unknown" while
 * the session is loading or could not be checked — checkout must then wait instead of sending an
 * order under a guessed identity.
 */
export function checkoutCsrf(state: SessionState): string | null | 'unknown' {
  switch (state.status) {
    case 'customer':
      return state.csrf
    case 'guest':
      return null
    default:
      return 'unknown'
  }
}

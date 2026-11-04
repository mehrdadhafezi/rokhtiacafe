import { createContext, useContext } from 'react'

import { isApiError } from '@/lib/api'

import { customerApi, type Customer, type CustomerSession } from './api'

/**
 * The customer's login state for the /menu app. The session cookie is HttpOnly; the CSRF token
 * lives only in memory (the React Query cache), never in storage, URLs or logs. Guests keep
 * ordering exactly as before; nothing here touches the staff session.
 */

export type SessionState =
  | { status: 'loading' }
  | { status: 'guest' }
  | { status: 'customer'; customer: Customer; csrf: string }

export interface SessionValue {
  state: SessionState
  /** The customer CSRF token, or null for guests (also null while loading). */
  csrf: string | null
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

/** GET /customer/auth/me; 401 means "guest", not an error. */
export async function loadSession(signal?: AbortSignal): Promise<CustomerSession | null> {
  try {
    return await customerApi.me(signal)
  } catch (err) {
    if (isApiError(err, 'UNAUTHENTICATED')) return null
    throw err
  }
}

export function sessionState(data: CustomerSession | null | undefined, pending: boolean): SessionState {
  if (pending) return { status: 'loading' }
  if (!data) return { status: 'guest' }
  return { status: 'customer', customer: data.customer, csrf: data.csrf_token }
}

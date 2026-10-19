import { useQueryClient } from '@tanstack/react-query'
import { createContext, useCallback, useContext, useState } from 'react'

import type { Order, User } from './api'
import { dashboardKey, inventoryKey, orderKey, ordersKey } from './keys'
import type { Action } from './permissions'

export interface AuthValue {
  user: User
  can: (action: Action) => boolean
  logout: () => void
}
export const AuthContext = createContext<AuthValue | null>(null)

export function useAuth(): AuthValue {
  const v = useContext(AuthContext)
  if (!v) throw new Error('useAuth outside AuthProvider')
  return v
}

export type ToastTone = 'success' | 'danger'
export const ToastContext = createContext<(text: string, tone?: ToastTone) => void>(() => {})
export const useToast = () => useContext(ToastContext)

function newKey() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const b = crypto.getRandomValues(new Uint8Array(16))
  b[6] = (b[6]! & 0x0f) | 0x40
  b[8] = (b[8]! & 0x3f) | 0x80
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('')
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

/**
 * Idempotency key for one form submission: retries of the same submission reuse it (the server
 * replays instead of recording twice); `rotate()` after success starts a new one.
 */
export function useIdempotencyKey() {
  const [key, setKey] = useState(newKey)
  const rotate = useCallback(() => setKey(newKey()), [])
  return [key, rotate] as const
}

/** Refresh everything an order change can affect (board, dashboard counts, stock). */
export function useRefreshOrders() {
  const queryClient = useQueryClient()
  return (order?: Pick<Order, 'id'>) => {
    void queryClient.invalidateQueries({ queryKey: ordersKey })
    void queryClient.invalidateQueries({ queryKey: dashboardKey })
    void queryClient.invalidateQueries({ queryKey: inventoryKey })
    if (order) void queryClient.invalidateQueries({ queryKey: orderKey(order.id) })
  }
}

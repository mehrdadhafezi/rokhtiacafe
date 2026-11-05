/**
 * Customer account API (/customer/*). Customers have their own HttpOnly session cookie and their
 * own CSRF token: every unsafe call passes the customer token explicitly (`csrf`), so the staff
 * token kept by lib/api is never sent here. Responses are `no-store` on the server.
 */
import { api } from '@/lib/api'

import type { OrderStatus } from '@/features/menu/api'

export interface Customer {
  id: number
  first_name: string
  last_name: string
  phone: string
  /** Always false until SMS/OTP verification exists: never present the phone as verified. */
  phone_verified: boolean
}

export interface CustomerSession {
  customer: Customer
  csrf_token: string
}

export interface RegisterBody {
  first_name: string
  last_name: string
  phone: string
  password: string
}

export interface HistoryItem {
  name: string
  quantity: number
  unit_price_rial: number
  line_total_rial: number
}

export interface HistoryPayment {
  kind: 'PAYMENT' | 'REFUND'
  method: 'CASH' | 'CARD_TERMINAL'
  amount_rial: number
  created_at: string
}

export interface HistoryOrder {
  id: number
  order_number: number
  business_date: string
  status: OrderStatus
  payment_status: 'UNPAID' | 'PAID' | 'REFUNDED' | ''
  total_rial: number
  paid_rial: number
  refunded_rial: number
  customer_label: string | null
  customer_note: string | null
  expired: boolean
  created_at: string
  completed_at: string | null
  cancelled_at: string | null
  cancel_reason: 'CUSTOMER' | 'STAFF' | 'EXPIRED' | null
  items: HistoryItem[]
  payments?: HistoryPayment[]
}

export interface Page<T> {
  items: T[]
  next_before_id: number | null
}

export type ActivityType = 'REGISTERED' | 'LOGIN_SUCCEEDED' | 'LOGOUT' | 'PASSWORD_CHANGED' | 'PROFILE_UPDATED' | 'ORDER_CREATED' | 'SESSIONS_REVOKED'

export interface Activity {
  id: number
  type: ActivityType
  created_at: string
  order_id: number | null
  order_number: number | null
}

export const customerKey = ['customer'] as const
export const meKey = ['customer', 'me'] as const
export const historyKey = ['customer', 'orders'] as const
export const historyOrderKey = (id: number) => ['customer', 'orders', id] as const
export const activityKey = ['customer', 'activity'] as const

export const customerApi = {
  me: (signal?: AbortSignal) => api.get<CustomerSession>('/customer/auth/me', { signal, csrf: null }),
  // Login and registration carry no session yet: the server checks the Origin instead.
  login: (phone: string, password: string) => api.post<CustomerSession>('/customer/auth/login', { phone, password }, { csrf: null }),
  register: (body: RegisterBody) => api.post<CustomerSession>('/customer/auth/register', body, { csrf: null }),
  logout: (csrf: string | null) => api.post<void>('/customer/auth/logout', undefined, { csrf }),
  changePassword: (csrf: string | null, current_password: string, new_password: string) =>
    api.post<CustomerSession>('/customer/auth/password', { current_password, new_password }, { csrf }),
  orders: (beforeId: number | null, signal?: AbortSignal) =>
    api.get<Page<HistoryOrder>>(`/customer/orders?limit=20${beforeId ? `&before_id=${beforeId}` : ''}`, { signal, csrf: null }),
  order: (id: number, signal?: AbortSignal) => api.get<HistoryOrder>(`/customer/orders/${id}`, { signal, csrf: null }),
  activity: (signal?: AbortSignal) => api.get<Page<Activity>>('/customer/activity?limit=20', { signal, csrf: null }),
}

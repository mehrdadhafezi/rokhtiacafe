/**
 * Public customer API (docs/API-CONTRACT.md §3). No cookies are needed; the order tracking
 * token travels only in the `X-Tracking-Token` header (SECURITY §10).
 */
import { api } from '@/lib/api'

export interface MenuItem {
  id: number
  name: string
  description: string | null
  price_rial: number
  image_url: string | null
  available: boolean
}

export interface MenuCategory {
  id: number
  name: string
  products: MenuItem[]
}

export interface Ordering {
  open: boolean
  opens_at: string
  closes_at: string
}

export interface MenuResponse {
  ordering: Ordering
  categories: MenuCategory[]
}

export type OrderStatus = 'PENDING' | 'PREPARING' | 'READY' | 'COMPLETED' | 'CANCELLED'

export interface PublicOrderItem {
  product_id: number
  name: string
  quantity: number
  unit_price_rial: number
  line_total_rial: number
}

export interface PublicOrder {
  order_number: number
  /** Only present in the create (and replay) response. */
  tracking_token?: string
  status: OrderStatus
  payment_status: 'UNPAID' | 'PAID' | 'REFUNDED'
  total_rial: number
  customer_label: string | null
  expires_at: string
  expired: boolean
  created_at: string
  ready_at: string | null
  completed_at: string | null
  cancel_reason: 'CUSTOMER' | 'STAFF' | 'EXPIRED' | null
  items: PublicOrderItem[]
}

export interface OrderLine {
  product_id: number
  quantity: number
  expected_unit_price_rial: number
}

export interface CreateOrderBody {
  customer_label?: string
  customer_note?: string
  items: OrderLine[]
}

export const menuQueryKey = ['public', 'menu'] as const
export const orderQueryKey = (token: string) => ['public', 'order', token] as const

export const fetchMenu = (signal?: AbortSignal) => api.get<MenuResponse>('/public/menu', { signal })

export const createOrder = (body: CreateOrderBody, idempotencyKey: string, clientId: string) =>
  api.post<PublicOrder>('/public/orders', body, { headers: { 'Idempotency-Key': idempotencyKey, 'X-Client-Id': clientId } })

export const fetchOrder = (token: string, signal?: AbortSignal) =>
  api.get<PublicOrder>('/public/order', { headers: { 'X-Tracking-Token': token }, signal })

export const cancelOrder = (token: string) => api.post<PublicOrder>('/public/order/cancel', undefined, { headers: { 'X-Tracking-Token': token } })

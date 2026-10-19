import { api } from '@/lib/api'

import type {
  CancelReason,
  ExpenseCategory,
  InventoryMode,
  MovementType,
  OrderStatus,
  PaymentKind,
  PaymentMethod,
  PaymentStatus,
  ReasonCode,
  RoleName,
} from './labels'

/** Types and calls for /api/v1/auth and /api/v1/admin (docs/API-CONTRACT.md §4–5). */

export interface Paged<T> {
  items: T[]
  next_before_id?: number | null
}

// ---- auth ----

export interface User {
  id: number
  username: string
  display_name: string
  role: RoleName
}
export interface Session {
  user: User
  csrf_token: string
}

export const authApi = {
  me: (signal?: AbortSignal) => api.get<Session>('/auth/me', { signal }),
  login: (username: string, password: string) => api.post<Session>('/auth/login', { username, password }),
  logout: () => api.post<void>('/auth/logout'),
  changePassword: (current_password: string, new_password: string) =>
    api.post<void>('/auth/password', { current_password, new_password }),
}

// ---- dashboard ----

export interface Dashboard {
  open_counts: Record<'PENDING' | 'PREPARING' | 'READY', number>
  today_orders: number
  low_stock: { product_id: number; name: string; available: number; threshold: number }[]
  ordering_open: boolean
  ordering_switch_open: boolean
  ordering_hours: { open: boolean; opens_at: string; closes_at: string }
  today_revenue_rial?: number
}

export const dashboardApi = {
  get: (signal?: AbortSignal) => api.get<Dashboard>('/admin/dashboard', { signal }),
  setOrdering: (open: boolean) => api.put<{ switch_open: boolean; open_now: boolean }>('/admin/ordering', { open }),
}

// ---- orders & payments ----

export interface OrderItem {
  id: number
  product_id: number
  name: string
  inventory_mode: InventoryMode
  quantity: number
  unit_price_rial: number
  line_total_rial: number
  wasted_quantity: number
  cogs_rial?: number
  estimated_cost_rial?: number
}

export interface Payment {
  id: number
  order_id: number
  kind: PaymentKind
  method: PaymentMethod
  amount_rial: number
  refund_of_payment_id: number | null
  note: string | null
  created_by: number
  created_at: string
  voided_at: string | null
  voided_by: number | null
  void_reason: string | null
}

export interface Order {
  id: number
  order_number: number
  business_date: string
  status: OrderStatus
  payment_status: PaymentStatus
  total_rial: number
  paid_rial: number
  refunded_rial: number
  customer_label: string | null
  customer_note: string | null
  expires_at: string
  created_at: string
  accepted_at: string | null
  ready_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  cancel_reason: CancelReason | null
  cancel_note: string | null
  items: OrderItem[]
  payments: Payment[]
}

export interface RefundLine {
  method: PaymentMethod
  amount_rial: number
}

export interface OrderMoney {
  order_id: number
  status: OrderStatus
  payment_status: PaymentStatus
  total_rial: number
  paid_rial: number
  refunded_rial: number
  remaining_rial: number
}

export type Transition = 'accept' | 'ready' | 'complete'

export const ordersApi = {
  list: (params: { status?: OrderStatus[]; date?: string; before_id?: number; limit?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams()
    if (params.status?.length) q.set('status', params.status.join(','))
    if (params.date) q.set('date', params.date)
    if (params.before_id) q.set('before_id', String(params.before_id))
    q.set('limit', String(params.limit ?? 100))
    return api.get<Paged<Order>>(`/admin/orders?${q}`, { signal })
  },
  get: (id: number, signal?: AbortSignal) => api.get<Order>(`/admin/orders/${id}`, { signal }),
  transition: (id: number, t: Transition) => api.post<Order>(`/admin/orders/${id}/${t}`),
  cancel: (id: number, body: { note?: string; waste?: { product_id: number; quantity: number }[] }) =>
    api.post<Order & { refunds: RefundLine[] }>(`/admin/orders/${id}/cancel`, body),
  pay: (id: number, body: { method: PaymentMethod; amount_rial: number; note?: string }, idempotencyKey: string) =>
    api.post<{ payment: Payment; order: OrderMoney }>(`/admin/orders/${id}/payments`, body, {
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
  refund: (id: number, note: string) =>
    api.post<{ refunds: Payment[]; order: OrderMoney }>(`/admin/orders/${id}/refund`, { note }),
  voidPayment: (paymentId: number, reason: string) =>
    api.post<{ payment: Payment; order: OrderMoney }>(`/admin/payments/${paymentId}/void`, { reason }),
}

// ---- catalog ----

export interface Category {
  id: number
  name: string
  sort_order: number
  is_active: boolean
}

export interface Product {
  id: number
  category_id: number
  name: string
  description: string | null
  price_rial: number
  inventory_mode: InventoryMode
  low_stock_threshold: number | null
  estimated_unit_cost_rial?: number | null
  is_active: boolean
  is_available: boolean
  image_url: string | null
  sort_order: number
}

export interface ProductBody {
  category_id?: number
  name?: string
  description?: string | null
  price_rial?: number
  inventory_mode?: InventoryMode
  low_stock_threshold?: number | null
  estimated_unit_cost_rial?: number | null
  is_active?: boolean
  sort_order?: number
}

export const catalogApi = {
  categories: (signal?: AbortSignal) => api.get<Paged<Category>>('/admin/categories', { signal }),
  createCategory: (body: { name: string; sort_order?: number }) => api.post<Category>('/admin/categories', body),
  updateCategory: (id: number, body: Partial<Pick<Category, 'name' | 'sort_order' | 'is_active'>>) =>
    api.patch<Category>(`/admin/categories/${id}`, body),
  products: (signal?: AbortSignal) => api.get<Paged<Product>>('/admin/products?include_inactive=true', { signal }),
  createProduct: (body: ProductBody) => api.post<Product>('/admin/products', body),
  updateProduct: (id: number, body: ProductBody) => api.patch<Product>(`/admin/products/${id}`, body),
  setAvailability: (id: number, is_available: boolean) =>
    api.patch<Product>(`/admin/products/${id}/availability`, { is_available }),
  uploadImage: (id: number, file: File) => {
    const form = new FormData()
    form.set('file', file)
    return api.put<Product>(`/admin/products/${id}/image`, form)
  },
  deleteImage: (id: number) => api.delete<Product>(`/admin/products/${id}/image`),
}

// ---- inventory & purchases ----

export interface StockRow {
  product_id: number
  name: string
  is_active: boolean
  on_hand: number
  reserved: number
  available: number
  threshold: number | null
  low: boolean
  avg_unit_cost_rial?: number
  inventory_value_rial?: number
  last_unit_cost_rial?: number
}

export interface Movement {
  id: number
  product_id: number
  movement_type: MovementType
  reason_code: ReasonCode | null
  quantity_delta: number
  value_delta_rial: number
  on_hand_after: number
  value_after_rial: number
  order_id: number | null
  purchase_entry_id: number | null
  note: string | null
  created_at: string
}

export interface Purchase {
  id: number
  product_id: number
  quantity: number
  unit_cost_rial: number
  total_cost_rial: number
  supplier: string | null
  purchased_on: string
  note: string | null
  created_at: string
  voided_at: string | null
  void_reason: string | null
}

export interface AdjustBody {
  direction: 'IN' | 'OUT'
  quantity: number
  unit_cost_rial?: number
  reason_code: ReasonCode
  note: string
}

export interface PurchaseBody {
  product_id: number
  quantity: number
  unit_cost_rial: number
  purchased_on: string
  supplier?: string
  note?: string
}

const idem = (key: string) => ({ headers: { 'Idempotency-Key': key } })

export const inventoryApi = {
  list: (signal?: AbortSignal) => api.get<Paged<StockRow>>('/admin/inventory', { signal }),
  movements: (productId: number, beforeId?: number, signal?: AbortSignal) =>
    api.get<Paged<Movement>>(
      `/admin/inventory/${productId}/movements?limit=50${beforeId ? `&before_id=${beforeId}` : ''}`,
      { signal },
    ),
  adjust: (productId: number, body: AdjustBody, key: string) =>
    api.post<Movement>(`/admin/inventory/${productId}/adjustments`, body, idem(key)),
  purchases: (params: { product_id?: number; from?: string; to?: string; before_id?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams({ limit: '100' })
    for (const [k, v] of Object.entries(params)) if (v) q.set(k, String(v))
    return api.get<Paged<Purchase>>(`/admin/purchases?${q}`, { signal })
  },
  createPurchase: (body: PurchaseBody, key: string) => api.post<Purchase>('/admin/purchases', body, idem(key)),
  voidPurchase: (id: number, reason: string) => api.post<Purchase>(`/admin/purchases/${id}/void`, { reason }),
}

// ---- expenses & reports ----

export interface Expense {
  id: number
  category: ExpenseCategory
  amount_rial: number
  expense_date: string
  description: string
  created_at: string
  voided_at: string | null
  void_reason: string | null
}

export type ByMethod = Record<PaymentMethod, number>

export interface Figures {
  gross_sales_rial: number
  sales_refunds_rial: number
  revenue_rial: number
  orders: number
  average_order_value_rial: number
  cogs_rial: number
  gross_profit_rial: number
  inventory_losses_rial: number
  inventory_gains_rial: number
  expenses_rial: number
  operating_result_rial: number
  purchases_rial: number
  prepayment_refunds_rial: number
  payments_received: ByMethod
  refunds_recorded: ByMethod
  collected: ByMethod
  estimated_waste_rial: number
}

export interface Summary {
  from: string
  to: string
  totals: Figures
  daily: (Figures & { date: string })[]
  expenses_by_category: Partial<Record<ExpenseCategory, number>>
  receivables: { orders: number; amount_rial: number }
}

export interface ProductLine {
  product_id: number
  name: string
  inventory_mode: InventoryMode
  quantity: number
  revenue_rial: number
  cogs_rial: number
  gross_profit_rial: number
  estimated_revenue_rial: number | null
  estimated_cost_rial: number | null
  estimated_margin_rial: number | null
  lines_without_estimate: number
}

export interface Receivable {
  order_id: number
  order_number: number
  business_date: string
  completed_at: string
  total_rial: number
  paid_rial: number
  outstanding_rial: number
}

export const financeApi = {
  expenses: (params: { from?: string; to?: string; category?: ExpenseCategory; before_id?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams({ limit: '100' })
    for (const [k, v] of Object.entries(params)) if (v) q.set(k, String(v))
    return api.get<Paged<Expense>>(`/admin/expenses?${q}`, { signal })
  },
  createExpense: (body: { category: ExpenseCategory; amount_rial: number; expense_date: string; description: string }, key: string) =>
    api.post<Expense>('/admin/expenses', body, idem(key)),
  voidExpense: (id: number, reason: string) => api.post<Expense>(`/admin/expenses/${id}/void`, { reason }),
  summary: (from: string, to: string, signal?: AbortSignal) =>
    api.get<Summary>(`/admin/reports/summary?from=${from}&to=${to}`, { signal }),
  products: (from: string, to: string, signal?: AbortSignal) =>
    api.get<{ items: ProductLine[] }>(`/admin/reports/products?from=${from}&to=${to}`, { signal }),
  receivables: (signal?: AbortSignal) =>
    api.get<{ items: Receivable[]; total_outstanding_rial: number }>('/admin/reports/receivables', { signal }),
}

// ---- users ----

export interface AdminUser extends User {
  is_active: boolean
  last_login_at: string | null
  created_at: string
}

export const usersApi = {
  list: (signal?: AbortSignal) => api.get<Paged<AdminUser>>('/admin/users', { signal }),
  create: (body: { username: string; display_name: string; role: RoleName; password: string }) =>
    api.post<AdminUser>('/admin/users', body),
  update: (id: number, body: { display_name?: string; role?: RoleName; is_active?: boolean }) =>
    api.patch<AdminUser>(`/admin/users/${id}`, body),
  resetPassword: (id: number, new_password: string) => api.post<void>(`/admin/users/${id}/password`, { new_password }),
}

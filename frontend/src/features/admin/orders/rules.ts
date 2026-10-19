import type { Order, Transition } from '../api'
import type { OrderStatus } from '../labels'

/**
 * Which staff actions an order allows, mirroring the server rules (WORKFLOWS §2–4, API §5.2–5.3).
 * The server re-checks everything; these only decide which buttons are shown.
 */

export const boardStatuses = ['PENDING', 'PREPARING', 'READY'] as const satisfies readonly OrderStatus[]

export const nextTransition: Partial<Record<OrderStatus, { op: Transition; label: string }>> = {
  PENDING: { op: 'accept', label: 'پذیرش سفارش' },
  PREPARING: { op: 'ready', label: 'آماده شد' },
  READY: { op: 'complete', label: 'تحویل داده شد' },
}

/** Amount still to be received, in rials. */
export function remainingRial(o: Pick<Order, 'total_rial' | 'paid_rial'>): number {
  return Math.max(0, o.total_rial - o.paid_rial)
}

/** Payments are taken once the order is accepted, until it is fully paid; never after a refund. */
export function canRecordPayment(o: Pick<Order, 'status' | 'total_rial' | 'paid_rial' | 'refunded_rial'>): boolean {
  return (o.status === 'PREPARING' || o.status === 'READY' || o.status === 'COMPLETED') && o.refunded_rial === 0 && remainingRial(o) > 0
}

export function canCancel(o: Pick<Order, 'status'>): boolean {
  return o.status === 'PENDING' || o.status === 'PREPARING' || o.status === 'READY'
}

/** Waste can be recorded only for tracked items of an order already being prepared (F7). */
export function wasteableItems(o: Pick<Order, 'status' | 'items'>) {
  if (o.status !== 'PREPARING' && o.status !== 'READY') return []
  return o.items.filter((i) => i.inventory_mode === 'TRACKED')
}

/** Full refund of a completed, fully paid order (pre-completion refunds happen through cancel). */
export function canRefund(o: Pick<Order, 'status' | 'payment_status'>): boolean {
  return o.status === 'COMPLETED' && o.payment_status === 'PAID'
}

/** Voiding corrects a mistaken payment row; not on cancelled or refunded orders. */
export function canVoidPayment(o: Pick<Order, 'status' | 'refunded_rial'>, p: { kind: string; voided_at: string | null }): boolean {
  return p.kind === 'PAYMENT' && p.voided_at === null && o.status !== 'CANCELLED' && o.refunded_rial === 0
}

/** Whole seconds until a pending order expires (negative once past). */
export function secondsToExpiry(expiresAt: string, now: number = Date.now()): number {
  return Math.floor((Date.parse(expiresAt) - now) / 1000)
}

/** Board ordering: oldest first, so the next order to handle is on top. */
export function sortForBoard<T extends Pick<Order, 'created_at' | 'id'>>(orders: T[]): T[] {
  return [...orders].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at) || a.id - b.id)
}

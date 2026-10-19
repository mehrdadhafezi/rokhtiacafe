import type { OrderStatus, PublicOrder } from '@/features/menu/api'

/** Customer-facing status labels (PRD §5.1 C5). */
export const statusLabel: Record<OrderStatus, string> = {
  PENDING: 'در انتظار تأیید',
  PREPARING: 'در حال آماده‌سازی',
  READY: 'آماده تحویل',
  COMPLETED: 'تحویل شد',
  CANCELLED: 'لغو شد',
}

export const progressSteps: OrderStatus[] = ['PENDING', 'PREPARING', 'READY', 'COMPLETED']

/** Index of the current step in the progress bar, or -1 for a cancelled order. */
export function stepIndex(status: OrderStatus): number {
  return progressSteps.indexOf(status)
}

export function isFinal(order: Pick<PublicOrder, 'status' | 'expired'>): boolean {
  return order.status === 'COMPLETED' || order.status === 'CANCELLED' || order.expired
}

/** Pending orders are cancellable by the customer until accepted or expired (API §3). */
export function canCancel(order: Pick<PublicOrder, 'status' | 'expired'>): boolean {
  return order.status === 'PENDING' && !order.expired
}

export function cancelReasonText(order: Pick<PublicOrder, 'cancel_reason' | 'expired' | 'status'>): string | null {
  if (order.status === 'PENDING' && order.expired) return 'سفارش در مهلت تعیین‌شده تأیید نشد و منقضی شد.'
  if (order.status !== 'CANCELLED') return null
  switch (order.cancel_reason) {
    case 'CUSTOMER':
      return 'این سفارش را خودتان لغو کردید.'
    case 'EXPIRED':
      return 'سفارش در مهلت تعیین‌شده تأیید نشد و خودکار لغو شد.'
    default:
      return 'سفارش توسط کافه لغو شد. برای جزئیات با صندوق صحبت کنید.'
  }
}

/**
 * Payment line under the total. An unpaid order that will never be fulfilled (cancelled or
 * expired) shows nothing; a refund is only recorded — the customer collects it at the counter.
 */
export function paymentText(order: Pick<PublicOrder, 'payment_status' | 'status' | 'expired'>): string | null {
  switch (order.payment_status) {
    case 'PAID':
      return 'پرداخت شده'
    case 'REFUNDED':
      return 'بازپرداخت ثبت شده — مبلغ را از صندوق دریافت کنید'
    default:
      return order.status === 'CANCELLED' || order.expired ? null : 'پرداخت هنگام تحویل در صندوق'
  }
}

/** Polling interval for tracking: 5 s while active, none once final (decision D9). */
export function pollInterval(order: Pick<PublicOrder, 'status' | 'expired'> | undefined): number | false {
  if (!order) return 5_000
  return isFinal(order) ? false : 5_000
}

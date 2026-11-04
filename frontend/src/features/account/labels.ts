import type { ActivityType, HistoryOrder, HistoryPayment } from './api'

/** Persian labels for the customer's own history and activity. */

export const activityLabel: Record<ActivityType, string> = {
  REGISTERED: 'ساخت حساب',
  LOGIN_SUCCEEDED: 'ورود به حساب',
  LOGOUT: 'خروج از حساب',
  PASSWORD_CHANGED: 'تغییر رمز عبور',
  PROFILE_UPDATED: 'ویرایش مشخصات',
  ORDER_CREATED: 'ثبت سفارش',
  SESSIONS_REVOKED: 'بستن نشست‌ها',
}

export const paymentKindLabel: Record<HistoryPayment['kind'], string> = {
  PAYMENT: 'پرداخت',
  REFUND: 'بازپرداخت',
}

export const paymentMethodLabel: Record<HistoryPayment['method'], string> = {
  CASH: 'نقدی',
  CARD_TERMINAL: 'کارتخوان',
}

/** Status shown in the history list: an unaccepted order past its deadline reads as expired. */
export function historyStatus(o: Pick<HistoryOrder, 'status' | 'expired'>): { label: string; tone: 'neutral' | 'primary' | 'success' | 'danger' } {
  if (o.status === 'PENDING' && o.expired) return { label: 'منقضی شد', tone: 'danger' }
  switch (o.status) {
    case 'PENDING':
      return { label: 'در انتظار تأیید', tone: 'primary' }
    case 'PREPARING':
      return { label: 'در حال آماده‌سازی', tone: 'primary' }
    case 'READY':
      return { label: 'آماده تحویل', tone: 'primary' }
    case 'COMPLETED':
      return { label: 'تحویل شد', tone: 'success' }
    default:
      return { label: 'لغو شد', tone: 'danger' }
  }
}

/** Money line of a history order. A refund is recorded; the customer collects it at the counter. */
export function historyPaymentText(o: Pick<HistoryOrder, 'payment_status' | 'status' | 'expired'>): string | null {
  switch (o.payment_status) {
    case 'PAID':
      return 'پرداخت شده'
    case 'REFUNDED':
      return 'بازپرداخت شده'
    default:
      return o.status === 'CANCELLED' || o.expired ? null : 'پرداخت نشده'
  }
}

/** Persian labels for the API's English enum values; raw codes are never shown to staff. */

export type OrderStatus = 'PENDING' | 'PREPARING' | 'READY' | 'COMPLETED' | 'CANCELLED'
export type PaymentStatus = 'UNPAID' | 'PAID' | 'REFUNDED'
export type PaymentMethod = 'CASH' | 'CARD_TERMINAL'
export type PaymentKind = 'PAYMENT' | 'REFUND'
export type CancelReason = 'CUSTOMER' | 'STAFF' | 'EXPIRED'
export type InventoryMode = 'TRACKED' | 'UNTRACKED'
export type MovementType = 'PURCHASE' | 'PURCHASE_VOID' | 'SALE' | 'WASTE' | 'ADJUSTMENT_IN' | 'ADJUSTMENT_OUT'
export type ReasonIn = 'OPENING_BALANCE' | 'COUNT_CORRECTION' | 'RETURN_TO_STOCK'
export type ReasonOut = 'COUNT_CORRECTION' | 'DAMAGED' | 'EXPIRED'
export type ReasonCode = ReasonIn | ReasonOut
export type ExpenseCategory = 'INGREDIENTS' | 'SUPPLIES' | 'RENT' | 'UTILITIES' | 'SALARY' | 'MAINTENANCE' | 'OTHER'
export type RoleName = 'ADMIN' | 'OPERATOR'

export const orderStatusLabel: Record<OrderStatus, string> = {
  PENDING: 'در انتظار تأیید',
  PREPARING: 'در حال آماده‌سازی',
  READY: 'آماده تحویل',
  COMPLETED: 'تحویل شده',
  CANCELLED: 'لغو شده',
}

export const paymentStatusLabel: Record<PaymentStatus, string> = {
  UNPAID: 'پرداخت نشده',
  PAID: 'پرداخت شده',
  REFUNDED: 'بازپرداخت ثبت شده',
}

export const paymentMethodLabel: Record<PaymentMethod, string> = {
  CASH: 'نقدی',
  CARD_TERMINAL: 'کارتخوان',
}

export const paymentKindLabel: Record<PaymentKind, string> = {
  PAYMENT: 'دریافت',
  REFUND: 'بازپرداخت',
}

export const cancelReasonLabel: Record<CancelReason, string> = {
  CUSTOMER: 'لغو توسط مشتری',
  STAFF: 'لغو توسط کافه',
  EXPIRED: 'منقضی شده (تأیید نشد)',
}

export const inventoryModeLabel: Record<InventoryMode, string> = {
  TRACKED: 'با کنترل موجودی',
  UNTRACKED: 'بدون کنترل موجودی',
}

export const movementTypeLabel: Record<MovementType, string> = {
  PURCHASE: 'خرید',
  PURCHASE_VOID: 'ابطال خرید',
  SALE: 'فروش',
  WASTE: 'ضایعات',
  ADJUSTMENT_IN: 'اصلاح (افزایش)',
  ADJUSTMENT_OUT: 'اصلاح (کاهش)',
}

export const reasonLabel: Record<ReasonCode, string> = {
  OPENING_BALANCE: 'موجودی اول دوره',
  COUNT_CORRECTION: 'اصلاح شمارش',
  RETURN_TO_STOCK: 'برگشت به انبار',
  DAMAGED: 'آسیب‌دیده',
  EXPIRED: 'تاریخ‌گذشته',
}

export const reasonsIn: ReasonIn[] = ['OPENING_BALANCE', 'COUNT_CORRECTION', 'RETURN_TO_STOCK']
export const reasonsOut: ReasonOut[] = ['COUNT_CORRECTION', 'DAMAGED', 'EXPIRED']

export const expenseCategoryLabel: Record<ExpenseCategory, string> = {
  INGREDIENTS: 'مواد اولیه',
  SUPPLIES: 'ملزومات',
  RENT: 'اجاره',
  UTILITIES: 'قبوض',
  SALARY: 'حقوق',
  MAINTENANCE: 'تعمیرات',
  OTHER: 'سایر',
}

export const expenseCategories = Object.keys(expenseCategoryLabel) as ExpenseCategory[]

export const roleLabel: Record<RoleName, string> = {
  ADMIN: 'مدیر',
  OPERATOR: 'کارمند',
}

/** Persian text for VALIDATION_ERROR field names the forms send. */
export const fieldLabel: Record<string, string> = {
  name: 'نام',
  description: 'توضیحات',
  price_rial: 'قیمت',
  category_id: 'دسته‌بندی',
  inventory_mode: 'نوع موجودی',
  low_stock_threshold: 'حد هشدار موجودی',
  estimated_unit_cost_rial: 'هزینهٔ تخمینی',
  sort_order: 'ترتیب نمایش',
  username: 'نام کاربری',
  display_name: 'نام نمایشی',
  role: 'نقش',
  password: 'رمز عبور',
  new_password: 'رمز عبور جدید',
  current_password: 'رمز عبور فعلی',
  quantity: 'تعداد',
  unit_cost_rial: 'قیمت واحد',
  purchased_on: 'تاریخ خرید',
  supplier: 'تأمین‌کننده',
  note: 'یادداشت',
  reason: 'دلیل',
  reason_code: 'علت',
  amount_rial: 'مبلغ',
  expense_date: 'تاریخ هزینه',
  category: 'دسته',
  method: 'روش پرداخت',
  file: 'تصویر',
}

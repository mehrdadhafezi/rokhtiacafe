import { ApiError, type ApiErrorCode } from './api'

/** Persian messages for the stable API error codes (docs/API-CONTRACT.md §2). */
export const errorMessagesFa: Record<ApiErrorCode, string> = {
  BAD_REQUEST: 'درخواست نامعتبر است.',
  UNAUTHENTICATED: 'لطفاً دوباره وارد شوید.',
  FORBIDDEN: 'اجازهٔ انجام این کار را ندارید.',
  CSRF_FAILED: 'نشست شما منقضی شده است. صفحه را دوباره بارگذاری کنید.',
  NOT_FOUND: 'مورد درخواستی پیدا نشد.',
  ILLEGAL_TRANSITION: 'وضعیت سفارش تغییر کرده است و این کار دیگر ممکن نیست.',
  INSUFFICIENT_STOCK: 'موجودی برخی اقلام کافی نیست.',
  IDEMPOTENCY_KEY_REUSED: 'این درخواست قبلاً با اطلاعات دیگری ثبت شده است.',
  ORDER_EXPIRED: 'مهلت پذیرش این سفارش تمام شده و سفارش لغو شده است.',
  PRICE_CHANGED: 'قیمت برخی اقلام تغییر کرده است.',
  PURCHASE_ALREADY_CONSUMED: 'بخشی از این خرید مصرف شده و ابطال آن ممکن نیست.',
  PAYMENT_STATE_CONFLICT: 'وضعیت پرداخت این سفارش اجازهٔ این کار را نمی‌دهد.',
  CONFLICT: 'این مورد با اطلاعات موجود تداخل دارد.',
  PAYLOAD_TOO_LARGE: 'حجم فایل یا اطلاعات ارسالی بیش از حد مجاز است.',
  UNSUPPORTED_MEDIA_TYPE: 'فقط تصویرهای JPEG، PNG یا WebP پذیرفته می‌شوند.',
  VALIDATION_ERROR: 'برخی فیلدها نامعتبر هستند.',
  PRODUCT_UNAVAILABLE: 'برخی اقلام در حال حاضر موجود نیستند.',
  RATE_LIMITED: 'تعداد درخواست‌ها زیاد است. کمی بعد دوباره تلاش کنید.',
  TOO_MANY_OPEN_ORDERS: 'سفارش‌های در انتظار زیادی از این شبکه ثبت شده است. کمی صبر کنید.',
  ORDERING_CLOSED: 'در حال حاضر سفارش‌گیری بسته است.',
  ORDERING_BUSY: 'کافه در حال حاضر شلوغ است. چند دقیقهٔ دیگر دوباره تلاش کنید.',
  BUSY_RETRY: 'سرور مشغول است. دوباره تلاش کنید.',
  INTERNAL: 'خطای غیرمنتظره‌ای رخ داد. دوباره تلاش کنید.',
  NETWORK_ERROR: 'اتصال برقرار نشد. اینترنت را بررسی و دوباره تلاش کنید.',
  UNKNOWN: 'خطای غیرمنتظره‌ای رخ داد. دوباره تلاش کنید.',
}

/** User-facing Persian text for any thrown value. */
export function errorMessageFa(err: unknown): string {
  if (err instanceof ApiError) return errorMessagesFa[err.code] ?? errorMessagesFa.UNKNOWN
  return errorMessagesFa.UNKNOWN
}

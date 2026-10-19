import { ApiError } from '@/lib/api'
import { faDigits } from '@/lib/digits'
import { formatDate } from '@/lib/date'
import { errorMessageFa } from '@/lib/errors-fa'
import { apiDateToJalali, jalaliToApiDate, todayApiDate } from '@/lib/jalali'

import { fieldLabel } from './labels'

/** Rial amount as the text MoneyInput edits. */
export const rialToInput = (rial: number | null | undefined) => (rial == null ? '' : String(Math.round(rial / 10)))

/** «۰۸:۰۰» from an `HH:MM` setting. */
export const faClock = (hhmm: string) =>
  `${faDigits(Number(hhmm.slice(0, 2))).padStart(2, '۰')}:${faDigits(Number(hhmm.slice(3, 5))).padStart(2, '۰')}`

/** Persian message for a failed request, naming the invalid fields of a VALIDATION_ERROR. */
export function formError(err: unknown): string {
  if (err instanceof ApiError && err.code === 'VALIDATION_ERROR') {
    const fields = Object.keys((err.details.fields as Record<string, string> | undefined) ?? {})
    const names = fields.map((f) => fieldLabel[f.replace(/\[\d+\].*$/, '')] ?? fieldLabel[f.split('.').pop() ?? ''] ?? 'یک فیلد')
    if (names.length) return `${errorMessageFa(err)} (${[...new Set(names)].join('، ')})`
  }
  if (err instanceof ApiError && err.code === 'UNKNOWN' && err.status === 409) return 'این کار با وضعیت فعلی ممکن نیست.'
  return errorMessageFa(err)
}

/** «۲۷ مهر ۱۴۰۵» for an API date (`YYYY-MM-DD`, a calendar day in Tehran). */
export const formatApiDate = (apiDate: string) => formatDate(`${apiDate}T12:00:00Z`)

/** First day of the current Jalali month, as an API date. */
export function monthStartApiDate(): string {
  const t = apiDateToJalali(todayApiDate())
  return jalaliToApiDate(t.year, t.month, 1)
}

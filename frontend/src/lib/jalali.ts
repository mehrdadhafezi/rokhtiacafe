import { jalaliParts, toApiDate, type JalaliDate } from './date'

/**
 * Jalali ⇄ API date (`YYYY-MM-DD`, Asia/Tehran) conversion built on the browser's Persian calendar
 * (Intl), so no calendar tables are shipped. Reports and date inputs use these to turn a Jalali day
 * or month into the Gregorian range the API expects.
 */

const DAY = 86_400_000

const ordinal = (d: JalaliDate) => d.year * 372 + (d.month - 1) * 31 + d.day

/** Noon UTC of an API date: the same calendar day in Tehran (UTC+3:30), whatever the DST rules. */
function noonUtc(apiDate: string): number {
  const [y, m, d] = apiDate.split('-').map(Number) as [number, number, number]
  return Date.UTC(y, m - 1, d, 12)
}

/** `YYYY-MM-DD` for a Jalali date. The day is clamped to the month's length. */
export function jalaliToApiDate(year: number, month: number, day: number): string {
  const target = { year, month, day: Math.min(day, month <= 6 ? 31 : 30) }
  // Farvardin 1 falls on 20/21 March; start from an estimate and walk to the exact day.
  const offset = (month <= 6 ? (month - 1) * 31 : 186 + (month - 7) * 30) + target.day - 1
  let t = Date.UTC(year + 621, 2, 21 + offset, 12)
  for (let i = 0; i < 6; i++) {
    const diff = ordinal(target) - ordinal(jalaliParts(t))
    if (diff === 0) return toApiDate(t)
    t += Math.sign(diff) * Math.max(1, Math.min(Math.abs(diff), 3)) * DAY
  }
  // Esfand 30 in a common year does not exist: fall back to the last day of the month.
  return day > 29 ? jalaliToApiDate(year, month, day - 1) : toApiDate(t)
}

/** Jalali date of an API date. */
export function apiDateToJalali(apiDate: string): JalaliDate {
  return jalaliParts(noonUtc(apiDate))
}

export function daysInJalaliMonth(year: number, month: number): number {
  if (month <= 6) return 31
  if (month <= 11) return 30
  const next = noonUtc(jalaliToApiDate(year + 1, 1, 1))
  const first = noonUtc(jalaliToApiDate(year, 12, 1))
  return Math.round((next - first) / DAY)
}

/** Inclusive API date range of a Jalali month, for report requests. */
export function jalaliMonthRange(year: number, month: number): { from: string; to: string } {
  return { from: jalaliToApiDate(year, month, 1), to: jalaliToApiDate(year, month, daysInJalaliMonth(year, month)) }
}

export function shiftJalaliMonth(year: number, month: number, delta: number): { year: number; month: number } {
  const i = year * 12 + (month - 1) + delta
  return { year: Math.floor(i / 12), month: (i % 12) + 1 }
}

/** Today in Tehran, as an API date. */
export function todayApiDate(now: Date = new Date()): string {
  return toApiDate(now)
}

export const jalaliMonthNames = [
  'فروردین',
  'اردیبهشت',
  'خرداد',
  'تیر',
  'مرداد',
  'شهریور',
  'مهر',
  'آبان',
  'آذر',
  'دی',
  'بهمن',
  'اسفند',
] as const

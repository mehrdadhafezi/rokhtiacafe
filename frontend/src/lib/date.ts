/**
 * Dates: the API speaks RFC 3339 UTC timestamps and `YYYY-MM-DD` dates interpreted in
 * Asia/Tehran; the UI shows the Jalali calendar with Persian digits (decision D10).
 */

export const TIME_ZONE = 'Asia/Tehran'

const LOCALE = 'fa-IR-u-ca-persian'

const dateFmt = new Intl.DateTimeFormat(LOCALE, { timeZone: TIME_ZONE, year: 'numeric', month: 'long', day: 'numeric' })
const shortDateFmt = new Intl.DateTimeFormat(LOCALE, {
  timeZone: TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})
const timeFmt = new Intl.DateTimeFormat(LOCALE, { timeZone: TIME_ZONE, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
const monthFmt = new Intl.DateTimeFormat(LOCALE, { timeZone: TIME_ZONE, year: 'numeric', month: 'long' })
const jalaliPartsFmt = new Intl.DateTimeFormat('en-US-u-ca-persian-nu-latn', {
  timeZone: TIME_ZONE,
  year: 'numeric',
  month: 'numeric',
  day: 'numeric',
})
const apiDateFmt = new Intl.DateTimeFormat('en-CA', { timeZone: TIME_ZONE, year: 'numeric', month: '2-digit', day: '2-digit' })

export type DateInput = Date | string | number

function toDate(value: DateInput): Date {
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(d.getTime())) throw new RangeError(`invalid date: ${String(value)}`)
  return d
}

/** «۱ فروردین ۱۴۰۳» */
export function formatDate(value: DateInput): string {
  return dateFmt.format(toDate(value))
}

/** «۱۴۰۳/۰۱/۰۱» */
export function formatShortDate(value: DateInput): string {
  return shortDateFmt.format(toDate(value))
}

/** «۱۴:۳۰» */
export function formatTime(value: DateInput): string {
  return timeFmt.format(toDate(value))
}

/** «۱ فروردین ۱۴۰۳، ۱۴:۳۰» */
export function formatDateTime(value: DateInput): string {
  return `${formatDate(value)}، ${formatTime(value)}`
}

/** «فروردین ۱۴۰۳» — report month headings. Built from parts: ICU's fa pattern puts the year first. */
export function formatMonth(value: DateInput): string {
  const parts = monthFmt.formatToParts(toDate(value))
  const get = (type: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === type)?.value ?? ''
  return `${get('month')} ${get('year')}`
}

export interface JalaliDate {
  year: number
  month: number
  day: number
}

/** Numeric Jalali date in Tehran, e.g. for grouping reports by Jalali month. */
export function jalaliParts(value: DateInput): JalaliDate {
  const parts = jalaliPartsFmt.formatToParts(toDate(value))
  const get = (type: Intl.DateTimeFormatPartTypes) => Number.parseInt(parts.find((p) => p.type === type)?.value ?? '', 10)
  return { year: get('year'), month: get('month'), day: get('day') }
}

/** `YYYY-MM-DD` of the given instant in Asia/Tehran, for API date parameters. */
export function toApiDate(value: DateInput): string {
  return apiDateFmt.format(toDate(value))
}

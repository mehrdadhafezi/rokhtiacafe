import { describe, expect, it } from 'vitest'

import { formatDate, formatDateTime, formatMonth, formatShortDate, formatTime, jalaliParts, toApiDate } from './date'

// Nowruz 1403: 1 Farvardin 1403 = 20 March 2024.
const NOWRUZ_1403 = '2024-03-20T08:30:00Z' // 12:00 in Tehran (UTC+3:30)

describe('date (Jalali, Asia/Tehran)', () => {
  it('formats Jalali dates with Persian digits', () => {
    expect(formatDate(NOWRUZ_1403)).toBe('۱ فروردین ۱۴۰۳')
    expect(formatShortDate(NOWRUZ_1403)).toBe('۱۴۰۳/۰۱/۰۱')
    expect(formatMonth(NOWRUZ_1403)).toBe('فروردین ۱۴۰۳')
  })

  it('formats time in Tehran, 24-hour', () => {
    expect(formatTime(NOWRUZ_1403)).toBe('۱۲:۰۰')
    expect(formatTime('2024-03-19T21:00:00Z')).toBe('۰۰:۳۰')
    expect(formatDateTime(NOWRUZ_1403)).toBe('۱ فروردین ۱۴۰۳، ۱۲:۰۰')
  })

  it('uses Tehran, not UTC, for the calendar day', () => {
    // 21:00 UTC on 19 March is 00:30 on 20 March in Tehran.
    expect(jalaliParts('2024-03-19T21:00:00Z')).toEqual({ year: 1403, month: 1, day: 1 })
    expect(jalaliParts('2024-03-19T20:00:00Z')).toEqual({ year: 1402, month: 12, day: 29 })
    expect(toApiDate('2024-03-19T21:00:00Z')).toBe('2024-03-20')
    expect(toApiDate('2024-03-19T20:00:00Z')).toBe('2024-03-19')
  })

  it('accepts Date objects and epoch milliseconds', () => {
    const d = new Date(NOWRUZ_1403)
    expect(formatDate(d)).toBe('۱ فروردین ۱۴۰۳')
    expect(formatDate(d.getTime())).toBe('۱ فروردین ۱۴۰۳')
  })

  it('handles the last day of Esfand in a leap year', () => {
    // 1403 is a leap year: 30 Esfand 1403 = 20 March 2025.
    expect(jalaliParts('2025-03-20T08:30:00Z')).toEqual({ year: 1403, month: 12, day: 30 })
    expect(jalaliParts('2025-03-21T08:30:00Z')).toEqual({ year: 1404, month: 1, day: 1 })
  })

  it('rejects invalid input', () => {
    expect(() => formatDate('not a date')).toThrow(RangeError)
  })
})

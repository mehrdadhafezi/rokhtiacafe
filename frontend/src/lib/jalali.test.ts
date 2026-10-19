import { describe, expect, it } from 'vitest'

import {
  apiDateToJalali,
  daysInJalaliMonth,
  jalaliMonthRange,
  jalaliToApiDate,
  shiftJalaliMonth,
  todayApiDate,
} from './jalali'

describe('jalali conversion', () => {
  it('converts known dates both ways', () => {
    expect(jalaliToApiDate(1403, 1, 1)).toBe('2024-03-20')
    expect(jalaliToApiDate(1405, 7, 27)).toBe('2026-10-19')
    expect(jalaliToApiDate(1404, 12, 29)).toBe('2026-03-20')
    expect(apiDateToJalali('2026-10-19')).toEqual({ year: 1405, month: 7, day: 27 })
    expect(apiDateToJalali('2024-03-20')).toEqual({ year: 1403, month: 1, day: 1 })
  })

  it('round-trips every day of a year', () => {
    for (let m = 1; m <= 12; m++) {
      for (let d = 1; d <= daysInJalaliMonth(1404, m); d++) {
        expect(apiDateToJalali(jalaliToApiDate(1404, m, d))).toEqual({ year: 1404, month: m, day: d })
      }
    }
  })

  it('knows month lengths, including leap Esfand', () => {
    expect(daysInJalaliMonth(1404, 1)).toBe(31)
    expect(daysInJalaliMonth(1404, 7)).toBe(30)
    expect(daysInJalaliMonth(1403, 12)).toBe(30) // 1403 is a leap year
    expect(daysInJalaliMonth(1404, 12)).toBe(29)
    expect(jalaliToApiDate(1404, 12, 30)).toBe(jalaliToApiDate(1404, 12, 29)) // clamped
  })

  it('builds month ranges and shifts months across years', () => {
    expect(jalaliMonthRange(1405, 7)).toEqual({ from: '2026-09-23', to: '2026-10-22' })
    expect(shiftJalaliMonth(1405, 1, -1)).toEqual({ year: 1404, month: 12 })
    expect(shiftJalaliMonth(1404, 12, 1)).toEqual({ year: 1405, month: 1 })
  })

  it('uses the Tehran calendar day for today', () => {
    // 21:00 UTC is already the next day in Tehran (UTC+3:30).
    expect(todayApiDate(new Date('2026-10-19T21:00:00Z'))).toBe('2026-10-20')
  })
})

import { describe, expect, it } from 'vitest'

import { faDigits, toLatinDigits } from './digits'
import { formatToman, formatTomanNumber, parseTomanToRial, rialToToman, tomanToRial } from './money'

describe('money', () => {
  it('converts rials to tomans', () => {
    expect(rialToToman(125_000)).toBe(12_500)
    expect(rialToToman(0)).toBe(0)
    expect(rialToToman(15)).toBe(1.5)
  })

  it('converts whole tomans to rials', () => {
    expect(tomanToRial(12_500)).toBe(125_000)
    expect(() => tomanToRial(1.5)).toThrow(RangeError)
  })

  it('rejects non-integer or unsafe rial amounts', () => {
    expect(() => rialToToman(10.5)).toThrow(RangeError)
    expect(() => rialToToman(Number.MAX_SAFE_INTEGER + 1)).toThrow(RangeError)
    expect(() => rialToToman(Number.NaN)).toThrow(RangeError)
  })

  it('formats tomans with Persian digits and grouping', () => {
    expect(formatTomanNumber(125_000)).toBe('۱۲٬۵۰۰')
    expect(formatToman(125_000)).toBe('۱۲٬۵۰۰ تومان')
    expect(formatToman(0)).toBe('۰ تومان')
    expect(formatToman(15)).toBe('۱٫۵ تومان')
    expect(formatToman(12_345_678_900)).toBe('۱٬۲۳۴٬۵۶۷٬۸۹۰ تومان')
  })

  it('parses toman input in Persian, Arabic or Latin digits', () => {
    expect(parseTomanToRial('۱۲٬۵۰۰')).toBe(125_000)
    expect(parseTomanToRial('12,500')).toBe(125_000)
    expect(parseTomanToRial(' ١٢٥٠٠ ')).toBe(125_000)
    expect(parseTomanToRial('0')).toBe(0)
  })

  it('rejects malformed toman input', () => {
    for (const bad of ['', ' ', '-5', '12.5', '۱۲٫۵', 'abc', '1e5', '99999999999999999']) {
      expect(parseTomanToRial(bad), bad).toBeNull()
    }
  })
})

describe('digits', () => {
  it('normalizes Persian and Arabic digits', () => {
    expect(toLatinDigits('۰۱۲۳۴۵۶۷۸۹')).toBe('0123456789')
    expect(toLatinDigits('٠١٢٣٤٥٦٧٨٩')).toBe('0123456789')
    expect(toLatinDigits('سفارش ۴۲')).toBe('سفارش 42')
  })

  it('renders Persian digits without grouping', () => {
    expect(faDigits(1234)).toBe('۱۲۳۴')
  })
})

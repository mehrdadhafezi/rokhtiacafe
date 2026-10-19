import { toLatinDigits } from './digits'

/**
 * Money is stored and sent as integer rials (`*_rial`); the UI shows tomans
 * (1 toman = 10 rials) with Persian digits. Never do arithmetic on floats.
 */

const RIALS_PER_TOMAN = 10

const tomanFormat = new Intl.NumberFormat('fa-IR', { maximumFractionDigits: 1 })

function assertRial(rial: number) {
  if (!Number.isSafeInteger(rial)) throw new RangeError(`rial amount must be a safe integer, got ${rial}`)
}

/** Toman amount for display; has one decimal when rials are not a multiple of 10. */
export function rialToToman(rial: number): number {
  assertRial(rial)
  return rial / RIALS_PER_TOMAN
}

/** Integer rials for a whole-toman amount. */
export function tomanToRial(toman: number): number {
  if (!Number.isSafeInteger(toman)) throw new RangeError(`toman amount must be a safe integer, got ${toman}`)
  const rial = toman * RIALS_PER_TOMAN
  assertRial(rial)
  return rial
}

/** «۱۲٬۵۰۰» — tomans with Persian digits and grouping, no unit. */
export function formatTomanNumber(rial: number): string {
  return tomanFormat.format(rialToToman(rial))
}

/** «۱۲٬۵۰۰ تومان» */
export function formatToman(rial: number): string {
  return `${formatTomanNumber(rial)} تومان`
}

/**
 * Parse a whole-toman amount typed by a user («۱۲٬۵۰۰», "12,500", "12500") into rials.
 * Returns null for empty, negative, fractional or malformed input.
 */
export function parseTomanToRial(input: string): number | null {
  const normalized = toLatinDigits(input).replace(/[\s,٬،]/g, '')
  if (!/^\d+$/.test(normalized)) return null
  const rial = Number(normalized) * RIALS_PER_TOMAN
  return Number.isSafeInteger(rial) ? rial : null
}

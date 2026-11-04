import { z } from 'zod'

import { faDigits } from '@/lib/digits'

/**
 * Client-side mirror of the server's customer rules (internal/customer). The server decides;
 * these only give early, Persian feedback.
 */

export const MIN_PASSWORD = 10
export const MAX_PASSWORD = 128
export const MAX_NAME = 60

const PERSIAN_ZERO = 0x06f0
const ARABIC_ZERO = 0x0660

/** Canonical 09XXXXXXXXX form of an Iranian mobile number, or null (same rules as the server). */
export function normalizePhone(input: string): string | null {
  let digits = ''
  const s = input.trim()
  for (const ch of s) {
    const c = ch.codePointAt(0)!
    if (ch >= '0' && ch <= '9') digits += ch
    else if (c >= PERSIAN_ZERO && c <= PERSIAN_ZERO + 9) digits += String(c - PERSIAN_ZERO)
    else if (c >= ARABIC_ZERO && c <= ARABIC_ZERO + 9) digits += String(c - ARABIC_ZERO)
    else if (ch === '+' && digits === '') digits += '+'
    else if (ch === ' ' || ch === '-' || ch === ' ') continue
    else return null
  }
  if (digits.startsWith('+98')) digits = '0' + digits.slice(3)
  else if (digits.startsWith('0098')) digits = '0' + digits.slice(4)
  else if (digits.startsWith('98')) digits = '0' + digits.slice(2)
  return /^09\d{9}$/.test(digits) ? digits : null
}

const phone = z
  .string()
  .trim()
  .min(1, 'شمارهٔ موبایل را وارد کنید.')
  .refine((v) => normalizePhone(v) !== null, 'شمارهٔ موبایل معتبر نیست (مثل ۰۹۱۲۳۴۵۶۷۸۹).')
  .transform((v) => normalizePhone(v)!)

const name = (label: string) =>
  z
    .string()
    .trim()
    .min(1, `${label} را وارد کنید.`)
    .max(MAX_NAME, `${label} حداکثر ${faDigits(MAX_NAME)} نویسه است.`)
    .refine((v) => ![...v].some((c) => c.charCodeAt(0) < 32), `${label} نویسهٔ نامعتبر دارد.`)

const newPassword = z
  .string()
  .min(MIN_PASSWORD, `رمز عبور دست‌کم ${faDigits(MIN_PASSWORD)} نویسه باشد.`)
  .max(MAX_PASSWORD, `رمز عبور حداکثر ${faDigits(MAX_PASSWORD)} نویسه است.`)

export const loginSchema = z.object({
  phone,
  password: z.string().min(1, 'رمز عبور را وارد کنید.').max(4 * MAX_PASSWORD),
})

export const registerSchema = z
  .object({
    first_name: name('نام'),
    last_name: name('نام خانوادگی'),
    phone,
    password: newPassword,
    repeat: z.string(),
  })
  .refine((v) => v.password === v.repeat, { path: ['repeat'], message: 'تکرار رمز با رمز یکسان نیست.' })

export const passwordSchema = z
  .object({
    current_password: z.string().min(1, 'رمز فعلی را وارد کنید.'),
    new_password: newPassword,
    repeat: z.string(),
  })
  .refine((v) => v.new_password === v.repeat, { path: ['repeat'], message: 'تکرار رمز با رمز جدید یکسان نیست.' })

/**
 * Where to go after login: only same-app paths under /menu (never another origin, never a URL
 * that could carry a token), defaulting to the account page.
 */
export function safeReturnPath(from: unknown): string {
  if (typeof from !== 'string') return '/menu/account'
  if (!from.startsWith('/menu') || from.startsWith('//') || from.includes('\\') || from.includes('#')) return '/menu/account'
  if (from === '/menu/login' || from === '/menu/register') return '/menu/account'
  return from
}

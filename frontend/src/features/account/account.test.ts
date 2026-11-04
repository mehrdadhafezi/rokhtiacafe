import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createOrder } from '@/features/menu/api'
import { ApiError, setCsrfToken } from '@/lib/api'

import { customerApi } from './api'
import { historyPaymentText, historyStatus } from './labels'
import { loadSession, sessionState } from './session-context'
import { loginSchema, normalizePhone, passwordSchema, registerSchema, safeReturnPath } from './validation'

type Call = { url: string; init: RequestInit }
let calls: Call[]

function mockFetch(response: () => Response) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init })
      return response()
    }),
  )
}
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const csrfOf = (c: Call | undefined) => new Headers(c?.init.headers).get('X-CSRF-Token')

beforeEach(() => setCsrfToken(null))
afterEach(() => vi.unstubAllGlobals())

describe('normalizePhone (mirror of internal/customer.NormalizePhone)', () => {
  it.each([
    ['09123456789', '09123456789'],
    ['+989123456789', '09123456789'],
    ['989123456789', '09123456789'],
    ['00989123456789', '09123456789'],
    ['۰۹۱۲۳۴۵۶۷۸۹', '09123456789'],
    ['٠٩١٢٣٤٥٦٧٨٩', '09123456789'],
    ['0912 345 6789', '09123456789'],
    ['۰۹۱۲-۳۴۵-۶۷۸۹', '09123456789'],
    [' 09123456789 ', '09123456789'],
  ])('%s → %s', (input, want) => expect(normalizePhone(input)).toBe(want))

  it.each(['', '0912345678', '091234567890', '08123456789', '9123456789', 'abc', '0912345678x', '09+123456789', '0912.345.6789'])(
    'rejects %s',
    (input) => expect(normalizePhone(input)).toBeNull(),
  )
})

describe('forms', () => {
  it('register: names, phone, 10+ character password and a matching repeat', () => {
    const ok = registerSchema.safeParse({ first_name: ' مریم ', last_name: 'احمدی', phone: '۰۹۱۲۳۴۵۶۷۸۹', password: 'secure-cafe-26', repeat: 'secure-cafe-26' })
    expect(ok.success && ok.data).toMatchObject({ first_name: 'مریم', phone: '09123456789' })
    const bad = registerSchema.safeParse({ first_name: '', last_name: 'x'.repeat(61), phone: '0912', password: 'short', repeat: 'other' })
    expect(bad.success).toBe(false)
    const fields = bad.error!.issues.map((i) => i.path[0])
    expect(fields).toEqual(expect.arrayContaining(['first_name', 'last_name', 'phone', 'password']))
    // Messages use Persian digits only.
    for (const issue of bad.error!.issues) expect(issue.message).not.toMatch(/[0-9]/)
    const mismatch = registerSchema.safeParse({ first_name: 'a', last_name: 'b', phone: '09123456789', password: 'secure-cafe-26', repeat: 'secure-cafe-27' })
    expect(mismatch.success ? [] : mismatch.error.issues.map((i) => i.path[0])).toEqual(['repeat'])
  })

  it('login normalises the phone; password change needs a matching new password', () => {
    expect(loginSchema.parse({ phone: '+98 912 345 6789', password: 'x' }).phone).toBe('09123456789')
    expect(passwordSchema.safeParse({ current_password: 'a', new_password: 'new-secure-2027', repeat: 'new-secure-2027' }).success).toBe(true)
    expect(passwordSchema.safeParse({ current_password: 'a', new_password: 'new-secure-2027', repeat: 'x' }).success).toBe(false)
  })
})

describe('safeReturnPath', () => {
  it.each([
    ['/menu/my-orders', '/menu/my-orders'],
    ['/menu/my-orders/12', '/menu/my-orders/12'],
    ['/menu/cart', '/menu/cart'],
    [undefined, '/menu/account'],
    ['https://evil.example/menu', '/menu/account'],
    ['//evil.example/menu', '/menu/account'],
    ['/admin/', '/menu/account'],
    ['/menu\\..\\admin', '/menu/account'],
    ['/menu/order#token', '/menu/account'],
    ['/menu/login', '/menu/account'],
  ])('%s → %s', (from, want) => expect(safeReturnPath(from)).toBe(want))
})

describe('customer CSRF never mixes with the staff token', () => {
  it('sends the customer token on customer writes even when a staff token is known', async () => {
    mockFetch(() => new Response(null, { status: 204 }))
    setCsrfToken('STAFF')
    await customerApi.logout('CUSTOMER')
    expect(csrfOf(calls[0])).toBe('CUSTOMER')
    await customerApi.login('09123456789', 'x')
    await customerApi.register({ first_name: 'a', last_name: 'b', phone: '09123456789', password: 'secure-cafe-26' })
    expect(csrfOf(calls[1])).toBeNull()
    expect(csrfOf(calls[2])).toBeNull()
  })

  it('a guest order carries no token; a customer order carries the customer token', async () => {
    mockFetch(() => json(201, {}))
    setCsrfToken('STAFF')
    await createOrder({ items: [] }, 'key', 'client', null)
    await createOrder({ items: [] }, 'key', 'client', 'CUSTOMER')
    expect(csrfOf(calls[0])).toBeNull()
    expect(csrfOf(calls[1])).toBe('CUSTOMER')
    expect(calls[0]?.init.credentials).toBe('include')
    expect(new Headers(calls[1]?.init.headers).get('Idempotency-Key')).toBe('key')
  })

  it('history reads never send a CSRF token and keep ids in the path, not personal data', async () => {
    mockFetch(() => json(200, { items: [], next_before_id: null }))
    setCsrfToken('STAFF')
    await customerApi.orders(42)
    await customerApi.order(7)
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/customer/orders?limit=20&before_id=42', '/api/v1/customer/orders/7'])
  })
})

describe('session', () => {
  it('treats 401 from /me as a guest and other failures as errors', async () => {
    mockFetch(() => json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }))
    await expect(loadSession()).resolves.toBeNull()
    mockFetch(() => json(500, { error: { code: 'INTERNAL', message: 'x' } }))
    await expect(loadSession()).rejects.toBeInstanceOf(ApiError)
  })

  it('maps the /me result to a state', () => {
    const customer = { id: 1, first_name: 'مریم', last_name: 'احمدی', phone: '09123456789', phone_verified: false }
    expect(sessionState(undefined, true)).toEqual({ status: 'loading' })
    expect(sessionState(null, false)).toEqual({ status: 'guest' })
    expect(sessionState({ customer, csrf_token: 't' }, false)).toEqual({ status: 'customer', customer, csrf: 't' })
  })
})

describe('history labels', () => {
  it('shows expired, completed and cancelled states and honest payment text', () => {
    expect(historyStatus({ status: 'PENDING', expired: true }).label).toBe('منقضی شد')
    expect(historyStatus({ status: 'COMPLETED', expired: false }).tone).toBe('success')
    expect(historyStatus({ status: 'CANCELLED', expired: false }).tone).toBe('danger')
    expect(historyPaymentText({ payment_status: 'UNPAID', status: 'COMPLETED', expired: false })).toBe('پرداخت نشده')
    expect(historyPaymentText({ payment_status: 'UNPAID', status: 'CANCELLED', expired: false })).toBeNull()
    expect(historyPaymentText({ payment_status: 'REFUNDED', status: 'COMPLETED', expired: false })).toBe('بازپرداخت شده')
  })
})

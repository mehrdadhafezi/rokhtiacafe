import { QueryClient, QueryObserver } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createOrder } from '@/features/menu/api'
import { ApiError, setCsrfToken } from '@/lib/api'

import { customerApi } from './api'
import { historyPaymentText, historyStatus } from './labels'
import { checkoutCsrf, loadSession, SESSION_RETRIES, sessionQueryOptions, sessionState, type SessionState } from './session-context'
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
  const customer = { id: 1, first_name: 'مریم', last_name: 'احمدی', phone: '09123456789', phone_verified: false }
  const me = () => json(200, { customer, csrf_token: 'CUSTOMER' })

  it('treats any 401 from /me as a guest and other failures as errors', async () => {
    mockFetch(() => json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }))
    await expect(loadSession()).resolves.toBeNull()
    mockFetch(() => json(401, { error: { code: 'CUSTOMER_SESSION_EXPIRED', message: 'x' } }))
    await expect(loadSession()).resolves.toBeNull()
    mockFetch(() => json(500, { error: { code: 'INTERNAL', message: 'x' } }))
    await expect(loadSession()).rejects.toBeInstanceOf(ApiError)
  })

  it('maps the /me query to a state; a failure without an answer is never a guest', () => {
    expect(sessionState({ data: undefined, isError: false })).toEqual({ status: 'loading' })
    expect(sessionState({ data: undefined, isError: true })).toEqual({ status: 'error' })
    expect(sessionState({ data: null, isError: false })).toEqual({ status: 'guest' })
    expect(sessionState({ data: { customer, csrf_token: 't' }, isError: false })).toEqual({ status: 'customer', customer, csrf: 't' })
    // A failed background refresh keeps the last answer.
    expect(sessionState({ data: { customer, csrf_token: 't' }, isError: true }).status).toBe('customer')
    expect(sessionState({ data: null, isError: true }).status).toBe('guest')
  })

  it('checkout waits while the identity is loading or unknown', () => {
    const states: [SessionState, string | null][] = [
      [{ status: 'loading' }, 'unknown'],
      [{ status: 'error' }, 'unknown'],
      [{ status: 'guest' }, null],
      [{ status: 'customer', customer, csrf: 'CUSTOMER' }, 'CUSTOMER'],
    ]
    for (const [state, want] of states) expect(checkoutCsrf(state)).toBe(want)
  })

  // F-02 regression: the provider's real query options, driven by a QueryObserver. Before the fix a
  // 503 or a network failure of /me ended as "guest" while the browser still held a valid customer
  // cookie, so the cart offered guest checkout and the server refused the order (CSRF_FAILED).
  describe('provider query (F-02)', () => {
    let client: QueryClient
    beforeEach(() => {
      client = new QueryClient()
    })
    afterEach(() => client.clear())

    const observe = () => new QueryObserver(client, sessionQueryOptions(0))
    async function settle(observer: ReturnType<typeof observe>) {
      const unsubscribe = observer.subscribe(() => {})
      await vi.waitFor(() => expect(observer.getCurrentResult().isFetching).toBe(false))
      unsubscribe()
      const r = observer.getCurrentResult()
      return sessionState({ data: r.data, isError: r.isError })
    }

    it('401 → guest, answered once (no retries)', async () => {
      mockFetch(() => json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }))
      const state = await settle(observe())
      expect(state).toEqual({ status: 'guest' })
      expect(checkoutCsrf(state)).toBeNull()
      expect(calls).toHaveLength(1)
    })

    it('503 → error after the automatic retries, never guest; checkout is blocked', async () => {
      mockFetch(() => json(503, { error: { code: 'SERVICE_UNAVAILABLE', message: 'x' } }))
      const state = await settle(observe())
      expect(state).toEqual({ status: 'error' })
      expect(checkoutCsrf(state)).toBe('unknown')
      expect(calls).toHaveLength(1 + SESSION_RETRIES)
    })

    it('network failure → error, never guest', async () => {
      calls = []
      vi.stubGlobal(
        'fetch',
        vi.fn(async (url: string, init: RequestInit) => {
          calls.push({ url, init })
          throw new TypeError('Failed to fetch')
        }),
      )
      const state = await settle(observe())
      expect(state).toEqual({ status: 'error' })
      expect(checkoutCsrf(state)).toBe('unknown')
    })

    it('a transient failure recovers by itself through the automatic retry', async () => {
      let n = 0
      mockFetch(() => (n++ === 0 ? json(503, { error: { code: 'SERVICE_UNAVAILABLE', message: 'x' } }) : me()))
      const state = await settle(observe())
      expect(state).toEqual({ status: 'customer', customer, csrf: 'CUSTOMER' })
      expect(calls).toHaveLength(2)
    })

    it('"try again" after the error state restores the signed-in customer and its CSRF token', async () => {
      let down = true
      mockFetch(() => (down ? json(503, { error: { code: 'SERVICE_UNAVAILABLE', message: 'x' } }) : me()))
      const observer = observe()
      expect((await settle(observer)).status).toBe('error')
      down = false
      const unsubscribe = observer.subscribe(() => {})
      await observer.refetch() // what the retry button does
      unsubscribe()
      const r = observer.getCurrentResult()
      const state = sessionState({ data: r.data, isError: r.isError })
      expect(state).toEqual({ status: 'customer', customer, csrf: 'CUSTOMER' })
      expect(checkoutCsrf(state)).toBe('CUSTOMER')
      // /me never sends a CSRF token itself.
      expect(calls.every((c) => csrfOf(c) === null)).toBe(true)
    })

    it('a failed background refresh keeps the signed-in customer', async () => {
      mockFetch(me)
      const observer = observe()
      expect((await settle(observer)).status).toBe('customer')
      mockFetch(() => json(503, { error: { code: 'SERVICE_UNAVAILABLE', message: 'x' } }))
      const unsubscribe = observer.subscribe(() => {})
      await observer.refetch()
      unsubscribe()
      const r = observer.getCurrentResult()
      expect(r.isError).toBe(true)
      expect(sessionState({ data: r.data, isError: r.isError })).toEqual({ status: 'customer', customer, csrf: 'CUSTOMER' })
    })
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

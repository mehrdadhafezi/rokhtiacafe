import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api, ApiError, isApiError, setCsrfToken } from './api'

type Call = { url: string; init: RequestInit }

let calls: Call[]

function mockFetch(response: () => Response | Promise<Response>) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init })
      return response()
    }),
  )
}

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } })

const header = (call: Call | undefined, name: string) => new Headers(call?.init.headers).get(name)

beforeEach(() => setCsrfToken(null))
afterEach(() => vi.unstubAllGlobals())

describe('api', () => {
  it('prefixes /api/v1, sends cookies and parses JSON', async () => {
    mockFetch(() => json(200, { items: [1] }))
    await expect(api.get('/public/menu')).resolves.toEqual({ items: [1] })
    expect(calls[0]?.url).toBe('/api/v1/public/menu')
    expect(calls[0]?.init.method).toBe('GET')
    expect(calls[0]?.init.credentials).toBe('include')
  })

  it('serializes JSON bodies', async () => {
    mockFetch(() => json(200, {}))
    await api.post('/auth/login', { username: 'a', password: 'b' })
    expect(calls[0]?.init.body).toBe('{"username":"a","password":"b"}')
    expect(header(calls[0], 'Content-Type')).toBe('application/json')
  })

  it('passes FormData through without a JSON content type', async () => {
    mockFetch(() => json(200, {}))
    const form = new FormData()
    form.set('image', new Blob(['x'], { type: 'image/png' }), 'a.png')
    await api.post('/admin/products/1/image', form)
    expect(calls[0]?.init.body).toBe(form)
    expect(header(calls[0], 'Content-Type')).toBeNull()
  })

  it('adds X-CSRF-Token to unsafe methods only', async () => {
    mockFetch(() => new Response(null, { status: 204 }))
    setCsrfToken('tok')
    await api.get('/auth/me')
    await api.post('/auth/logout')
    await api.put('/admin/x', {})
    await api.patch('/admin/x', {})
    await api.delete('/admin/x')
    expect(calls.map((c) => header(c, 'X-CSRF-Token'))).toEqual([null, 'tok', 'tok', 'tok', 'tok'])
  })

  it('omits X-CSRF-Token when no token is known', async () => {
    mockFetch(() => new Response(null, { status: 204 }))
    await api.post('/public/order/cancel', undefined, { headers: { 'X-Tracking-Token': 't' } })
    expect(header(calls[0], 'X-CSRF-Token')).toBeNull()
    expect(header(calls[0], 'X-Tracking-Token')).toBe('t')
  })

  it('returns undefined for 204', async () => {
    mockFetch(() => new Response(null, { status: 204 }))
    await expect(api.post('/auth/logout')).resolves.toBeUndefined()
  })

  it('parses the error envelope', async () => {
    mockFetch(() =>
      json(
        409,
        { error: { code: 'INSUFFICIENT_STOCK', message: 'insufficient stock', details: { product_ids: [12] } } },
        { 'X-Request-Id': 'req-1' },
      ),
    )
    const err = await api.post('/public/orders', {}).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(isApiError(err, 'INSUFFICIENT_STOCK')).toBe(true)
    const apiErr = err as ApiError
    expect(apiErr.status).toBe(409)
    expect(apiErr.message).toBe('insufficient stock')
    expect(apiErr.details).toEqual({ product_ids: [12] })
    expect(apiErr.requestId).toBe('req-1')
  })

  it('reads Retry-After on 429', async () => {
    mockFetch(() => json(429, { error: { code: 'RATE_LIMITED', message: 'rate limited' } }, { 'Retry-After': '30' }))
    const err = (await api.get('/public/menu').catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe('RATE_LIMITED')
    expect(err.retryAfter).toBe(30)
    expect(err.details).toEqual({})
  })

  it('maps non-envelope responses to UNKNOWN', async () => {
    mockFetch(() => new Response('<html>bad gateway</html>', { status: 502 }))
    const err = (await api.get('/public/menu').catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe('UNKNOWN')
    expect(err.status).toBe(502)
  })

  it('maps fetch failures to NETWORK_ERROR', async () => {
    mockFetch(() => Promise.reject(new TypeError('Failed to fetch')))
    const err = (await api.get('/public/menu').catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe('NETWORK_ERROR')
    expect(err.status).toBe(0)
  })

  it('rethrows aborts unchanged', async () => {
    mockFetch(() => Promise.reject(new DOMException('aborted', 'AbortError')))
    await expect(api.get('/public/menu')).rejects.toMatchObject({ name: 'AbortError' })
  })
})

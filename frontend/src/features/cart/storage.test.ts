import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { checkoutKey, clearCheckoutKey, clientId, forgetOrder, recentOrders, rememberOrder } from './storage'

class MemoryStorage {
  private m = new Map<string, string>()
  getItem(k: string) {
    return this.m.get(k) ?? null
  }
  setItem(k: string, v: string) {
    this.m.set(k, v)
  }
  removeItem(k: string) {
    this.m.delete(k)
  }
}

beforeEach(() => {
  vi.stubGlobal('window', { localStorage: new MemoryStorage(), sessionStorage: new MemoryStorage() })
})
afterEach(() => vi.unstubAllGlobals())

describe('checkout idempotency key', () => {
  it('reuses the key for the same request and rotates it when the request changes', () => {
    const k1 = checkoutKey('A')
    expect(checkoutKey('A')).toBe(k1) // network retry → same key → the server replays the order
    const k2 = checkoutKey('B')
    expect(k2).not.toBe(k1)
    clearCheckoutKey()
    expect(checkoutKey('B')).not.toBe(k2)
    expect(k1).toMatch(/^[0-9a-f-]{36}$/)
  })

  it('keeps a stable client id', () => {
    expect(clientId()).toBe(clientId())
  })

  it('keeps working when storage throws (private mode)', () => {
    vi.stubGlobal('window', {
      get localStorage(): Storage {
        throw new Error('blocked')
      },
      get sessionStorage(): Storage {
        throw new Error('blocked')
      },
    })
    expect(checkoutKey('A')).toMatch(/^[0-9a-f-]{36}$/)
    expect(recentOrders()).toEqual([])
  })
})

describe('recent orders', () => {
  it('keeps orders for 24 hours, newest first, without duplicates', () => {
    const now = Date.now()
    rememberOrder({ token: 'a', number: 1, createdAt: new Date(now - 25 * 3600_000).toISOString() })
    rememberOrder({ token: 'b', number: 2, createdAt: new Date(now).toISOString() })
    rememberOrder({ token: 'b', number: 2, createdAt: new Date(now).toISOString() })
    expect(recentOrders(now).map((o) => o.token)).toEqual(['b'])
    forgetOrder('b')
    expect(recentOrders(now)).toEqual([])
  })
})

/**
 * Browser storage for the customer app. Every access is guarded: storage can be disabled
 * (private mode, blocked site data), and the app must keep working without it.
 */

function read(storage: () => Storage, key: string): string | null {
  try {
    return storage().getItem(key)
  } catch {
    return null
  }
}

function write(storage: () => Storage, key: string, value: string | null) {
  try {
    if (value === null) storage().removeItem(key)
    else storage().setItem(key, value)
  } catch {
    /* storage unavailable: keep going in memory */
  }
}

const local = () => window.localStorage
const session = () => window.sessionStorage

export const CART_KEY = 'rokh.cart.v1'
const CLIENT_ID_KEY = 'rokh.client_id'
const CHECKOUT_KEY = 'rokh.checkout.v1'
const ORDERS_KEY = 'rokh.orders.v1'

export const loadCart = () => read(local, CART_KEY)
export const saveCart = (json: string) => write(local, CART_KEY, json)

function uuid(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const b = new Uint8Array(16)
  crypto.getRandomValues(b)
  b[6] = (b[6]! & 0x0f) | 0x40
  b[8] = (b[8]! & 0x3f) | 0x80
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('')
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

/** A random per-device id, used only for rate limiting (`X-Client-Id`). */
export function clientId(): string {
  const existing = read(local, CLIENT_ID_KEY)
  if (existing) return existing
  const id = uuid()
  write(local, CLIENT_ID_KEY, id)
  return id
}

/**
 * The idempotency key for submitting this exact request. A retry of the same request (after a
 * timeout or network error) reuses the key, so the server replays instead of creating a second
 * order; any change to the request gets a fresh key.
 */
export function checkoutKey(fingerprint: string): string {
  try {
    const saved = JSON.parse(read(session, CHECKOUT_KEY) ?? 'null') as { fp: string; key: string } | null
    if (saved && saved.fp === fingerprint) return saved.key
  } catch {
    /* ignore malformed */
  }
  const key = uuid()
  write(session, CHECKOUT_KEY, JSON.stringify({ fp: fingerprint, key }))
  return key
}

export function clearCheckoutKey() {
  write(session, CHECKOUT_KEY, null)
}

export interface RecentOrder {
  token: string
  number: number
  createdAt: string
}

/** Orders placed from this device in the last 24 h (tracking tokens expire after that). */
export function recentOrders(now = Date.now()): RecentOrder[] {
  try {
    const list = JSON.parse(read(local, ORDERS_KEY) ?? '[]') as RecentOrder[]
    return list.filter(
      (o) => typeof o.token === 'string' && typeof o.number === 'number' && now - Date.parse(o.createdAt) < 24 * 3600 * 1000,
    )
  } catch {
    return []
  }
}

export function rememberOrder(order: RecentOrder) {
  const list = [order, ...recentOrders().filter((o) => o.token !== order.token)].slice(0, 5)
  write(local, ORDERS_KEY, JSON.stringify(list))
}

export function forgetOrder(token: string) {
  write(local, ORDERS_KEY, JSON.stringify(recentOrders().filter((o) => o.token !== token)))
}

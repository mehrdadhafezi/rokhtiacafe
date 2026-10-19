/**
 * Cart state for the customer app — pure functions only, so the rules are unit tested.
 *
 * Prices in the cart are the prices the customer saw. They are sent as
 * `expected_unit_price_rial`; the server never uses them for totals, it only rejects the order
 * with PRICE_CHANGED if they are stale (F13). Limits mirror the API (15 lines, 1–20 each).
 */
import type { MenuResponse, OrderLine } from '@/features/menu/api'

export const MAX_LINES = 15
export const MAX_QUANTITY = 20

export interface CartLine {
  productId: number
  name: string
  unitPriceRial: number
  imageUrl: string | null
  quantity: number
}

export interface Cart {
  lines: CartLine[]
  label: string
  note: string
}

export const emptyCart: Cart = { lines: [], label: '', note: '' }

export type CartAction =
  | { type: 'add'; item: { productId: number; name: string; unitPriceRial: number; imageUrl: string | null } }
  | { type: 'increment'; productId: number }
  | { type: 'decrement'; productId: number }
  | { type: 'remove'; productId: number }
  | { type: 'setLabel'; label: string }
  | { type: 'setNote'; note: string }
  | { type: 'applyPrices'; prices: Record<number, number> }
  | { type: 'removeMany'; productIds: number[] }
  | { type: 'clearItems' }
  | { type: 'replace'; cart: Cart }

export function cartReducer(cart: Cart, action: CartAction): Cart {
  switch (action.type) {
    case 'add': {
      const existing = cart.lines.find((l) => l.productId === action.item.productId)
      if (existing) return cartReducer(cart, { type: 'increment', productId: existing.productId })
      if (cart.lines.length >= MAX_LINES) return cart
      return { ...cart, lines: [...cart.lines, { ...action.item, quantity: 1 }] }
    }
    case 'increment':
      return {
        ...cart,
        lines: cart.lines.map((l) => (l.productId === action.productId ? { ...l, quantity: Math.min(MAX_QUANTITY, l.quantity + 1) } : l)),
      }
    case 'decrement':
      return {
        ...cart,
        lines: cart.lines
          .map((l) => (l.productId === action.productId ? { ...l, quantity: l.quantity - 1 } : l))
          .filter((l) => l.quantity > 0),
      }
    case 'remove':
      return { ...cart, lines: cart.lines.filter((l) => l.productId !== action.productId) }
    case 'setLabel':
      return { ...cart, label: action.label.slice(0, 40) }
    case 'setNote':
      return { ...cart, note: action.note.slice(0, 200) }
    case 'applyPrices':
      return {
        ...cart,
        lines: cart.lines.map((l) => {
          const price = action.prices[l.productId]
          return price === undefined ? l : { ...l, unitPriceRial: price }
        }),
      }
    case 'removeMany': {
      const drop = new Set(action.productIds)
      return { ...cart, lines: cart.lines.filter((l) => !drop.has(l.productId)) }
    }
    case 'clearItems':
      return { ...cart, lines: [], note: '' }
    case 'replace':
      return action.cart
  }
}

export function cartCount(cart: Cart): number {
  return cart.lines.reduce((n, l) => n + l.quantity, 0)
}

/** Total in integer rials, as shown before ordering. The server recomputes it. */
export function cartTotalRial(cart: Cart): number {
  return cart.lines.reduce((sum, l) => sum + l.unitPriceRial * l.quantity, 0)
}

export function quantityOf(cart: Cart, productId: number): number {
  return cart.lines.find((l) => l.productId === productId)?.quantity ?? 0
}

export interface PriceChange {
  productId: number
  name: string
  oldRial: number
  newRial: number
}

export interface Reconciliation {
  cart: Cart
  /** Lines whose price differs from the current menu — the customer must see the new price. */
  priceChanges: PriceChange[]
  /** Lines removed or flagged because the product is gone or sold out. */
  unavailable: { productId: number; name: string }[]
}

/**
 * Compare a stored cart with a fresh menu: refresh names and images, and report price changes
 * and unavailable products. Prices are NOT silently replaced — `priceChanges` lists them so the
 * UI can show old → new before the customer confirms.
 */
export function reconcileWithMenu(cart: Cart, menu: MenuResponse): Reconciliation {
  const byId = new Map(menu.categories.flatMap((c) => c.products.map((p) => [p.id, p] as const)))
  const priceChanges: PriceChange[] = []
  const unavailable: { productId: number; name: string }[] = []
  const lines = cart.lines.map((l) => {
    const p = byId.get(l.productId)
    if (!p || !p.available) {
      unavailable.push({ productId: l.productId, name: p?.name ?? l.name })
      return l
    }
    if (p.price_rial !== l.unitPriceRial) {
      priceChanges.push({ productId: l.productId, name: p.name, oldRial: l.unitPriceRial, newRial: p.price_rial })
    }
    return { ...l, name: p.name, imageUrl: p.image_url }
  })
  return { cart: { ...cart, lines }, priceChanges, unavailable }
}

/** The order request body; label and note are trimmed and omitted when empty. */
export function toOrderBody(cart: Cart): { customer_label?: string; customer_note?: string; items: OrderLine[] } {
  const label = cart.label.trim()
  const note = cart.note.trim()
  return {
    ...(label ? { customer_label: label } : {}),
    ...(note ? { customer_note: note } : {}),
    items: [...cart.lines]
      .sort((a, b) => a.productId - b.productId)
      .map((l) => ({ product_id: l.productId, quantity: l.quantity, expected_unit_price_rial: l.unitPriceRial })),
  }
}

/** Stable fingerprint of what would be submitted; a changed request needs a new idempotency key. */
export function requestFingerprint(cart: Cart): string {
  return JSON.stringify(toOrderBody(cart))
}

/** Parse a cart from storage, dropping anything malformed. */
export function parseStoredCart(raw: string | null): Cart {
  if (!raw) return emptyCart
  try {
    const v = JSON.parse(raw) as Partial<Cart>
    const lines = Array.isArray(v.lines)
      ? v.lines.filter(
          (l): l is CartLine =>
            !!l &&
            Number.isSafeInteger(l.productId) &&
            typeof l.name === 'string' &&
            Number.isSafeInteger(l.unitPriceRial) &&
            Number.isSafeInteger(l.quantity) &&
            l.quantity >= 1 &&
            l.quantity <= MAX_QUANTITY,
        )
      : []
    return {
      lines: lines.slice(0, MAX_LINES).map((l) => ({ ...l, imageUrl: typeof l.imageUrl === 'string' ? l.imageUrl : null })),
      label: typeof v.label === 'string' ? v.label.slice(0, 40) : '',
      note: typeof v.note === 'string' ? v.note.slice(0, 200) : '',
    }
  } catch {
    return emptyCart
  }
}

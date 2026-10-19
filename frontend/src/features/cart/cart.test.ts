import { describe, expect, it } from 'vitest'

import type { MenuResponse } from '@/features/menu/api'

import {
  MAX_LINES,
  MAX_QUANTITY,
  cartCount,
  cartReducer,
  cartTotalRial,
  emptyCart,
  parseStoredCart,
  reconcileWithMenu,
  requestFingerprint,
  toOrderBody,
  type Cart,
} from './cart'

const espresso = { productId: 12, name: 'اسپرسو', unitPriceRial: 950_000, imageUrl: null as string | null }
const cake = { productId: 3, name: 'کیک', unitPriceRial: 1_200_000, imageUrl: '/media/products/a.jpg' as string | null }

function cartWith(...items: (typeof espresso)[]): Cart {
  return items.reduce((c, item) => cartReducer(c, { type: 'add', item }), emptyCart)
}

describe('cartReducer', () => {
  it('adds, increments, decrements and removes lines', () => {
    let c = cartWith(espresso, espresso, cake)
    expect(c.lines.map((l) => [l.productId, l.quantity])).toEqual([
      [12, 2],
      [3, 1],
    ])
    expect(cartCount(c)).toBe(3)
    expect(cartTotalRial(c)).toBe(2 * 950_000 + 1_200_000)
    c = cartReducer(c, { type: 'decrement', productId: 3 })
    expect(c.lines).toHaveLength(1) // quantity 0 removes the line
    c = cartReducer(c, { type: 'remove', productId: 12 })
    expect(c.lines).toEqual([])
  })

  it('caps quantity at 20 and lines at 15', () => {
    let c = cartWith(espresso)
    for (let i = 0; i < 30; i++) c = cartReducer(c, { type: 'increment', productId: 12 })
    expect(c.lines[0]!.quantity).toBe(MAX_QUANTITY)
    let many = emptyCart
    for (let id = 1; id <= 20; id++) many = cartReducer(many, { type: 'add', item: { ...espresso, productId: id } })
    expect(many.lines).toHaveLength(MAX_LINES)
  })

  it('limits label and note length; clearing items keeps the label', () => {
    let c = cartReducer(cartWith(espresso), { type: 'setLabel', label: 'x'.repeat(60) })
    c = cartReducer(c, { type: 'setNote', note: 'y'.repeat(300) })
    expect(c.label).toHaveLength(40)
    expect(c.note).toHaveLength(200)
    c = cartReducer(c, { type: 'clearItems' })
    expect(c.lines).toEqual([])
    expect(c.note).toBe('')
    expect(c.label).toHaveLength(40)
  })

  it('applies confirmed prices only to the given products', () => {
    const c = cartReducer(cartWith(espresso, cake), { type: 'applyPrices', prices: { 12: 1_000_000 } })
    expect(c.lines.find((l) => l.productId === 12)!.unitPriceRial).toBe(1_000_000)
    expect(c.lines.find((l) => l.productId === 3)!.unitPriceRial).toBe(1_200_000)
  })
})

describe('reconcileWithMenu', () => {
  const menu = (o: { price?: number; available?: boolean; drop?: boolean } = {}): MenuResponse => ({
    ordering: { open: true, opens_at: '08:00', closes_at: '23:00' },
    categories: [
      {
        id: 1,
        name: 'قهوه',
        products: [
          ...(o.drop
            ? []
            : [
                {
                  id: 12,
                  name: 'اسپرسو دبل',
                  description: null,
                  price_rial: o.price ?? 950_000,
                  image_url: '/media/products/new.jpg',
                  available: o.available ?? true,
                },
              ]),
          { id: 3, name: 'کیک', description: null, price_rial: 1_200_000, image_url: null, available: true },
        ],
      },
    ],
  })

  it('refreshes names and images without touching prices', () => {
    const r = reconcileWithMenu(cartWith(espresso), menu())
    expect(r.priceChanges).toEqual([])
    expect(r.unavailable).toEqual([])
    expect(r.cart.lines[0]).toMatchObject({ name: 'اسپرسو دبل', imageUrl: '/media/products/new.jpg', unitPriceRial: 950_000 })
  })

  it('reports price changes instead of silently applying them (F13)', () => {
    const r = reconcileWithMenu(cartWith(espresso), menu({ price: 1_100_000 }))
    expect(r.priceChanges).toEqual([{ productId: 12, name: 'اسپرسو دبل', oldRial: 950_000, newRial: 1_100_000 }])
    expect(r.cart.lines[0]!.unitPriceRial).toBe(950_000)
  })

  it('flags sold-out and removed products', () => {
    expect(reconcileWithMenu(cartWith(espresso), menu({ available: false })).unavailable).toEqual([{ productId: 12, name: 'اسپرسو دبل' }])
    expect(reconcileWithMenu(cartWith(espresso), menu({ drop: true })).unavailable).toEqual([{ productId: 12, name: 'اسپرسو' }])
  })
})

describe('order body', () => {
  it('sends expected prices, sorted lines and trimmed optional text', () => {
    const c = cartReducer(cartWith(espresso, cake), { type: 'setLabel', label: '  سارا ' })
    expect(toOrderBody(c)).toEqual({
      customer_label: 'سارا',
      items: [
        { product_id: 3, quantity: 1, expected_unit_price_rial: 1_200_000 },
        { product_id: 12, quantity: 1, expected_unit_price_rial: 950_000 },
      ],
    })
  })

  it('changes the fingerprint whenever the request changes', () => {
    const a = cartWith(espresso)
    expect(requestFingerprint(a)).toBe(requestFingerprint(cartWith(espresso)))
    expect(requestFingerprint(a)).not.toBe(requestFingerprint(cartReducer(a, { type: 'increment', productId: 12 })))
    expect(requestFingerprint(a)).not.toBe(requestFingerprint(cartReducer(a, { type: 'applyPrices', prices: { 12: 1 } })))
    expect(requestFingerprint(a)).not.toBe(requestFingerprint(cartReducer(a, { type: 'setNote', note: 'بدون شکر' })))
  })
})

describe('parseStoredCart', () => {
  it('restores a valid cart and drops malformed lines', () => {
    const raw = JSON.stringify({
      lines: [
        { productId: 12, name: 'اسپرسو', unitPriceRial: 950000, imageUrl: null, quantity: 2 },
        { productId: 'x', name: 'bad' },
        { productId: 5, name: 'big', unitPriceRial: 10, imageUrl: null, quantity: 99 },
      ],
      label: 'سارا',
      note: 123,
    })
    const c = parseStoredCart(raw)
    expect(c.lines).toHaveLength(1)
    expect(c.label).toBe('سارا')
    expect(c.note).toBe('')
  })

  it('falls back to an empty cart on garbage', () => {
    expect(parseStoredCart('{not json')).toEqual(emptyCart)
    expect(parseStoredCart(null)).toEqual(emptyCart)
  })
})

import { expect } from '@playwright/test'

import { adminApi } from './helpers'

/** Catalog of the throwaway database: one untracked and one stocked product. */
export const PRODUCTS = { espresso: 'اسپرسو', cake: 'کیک شکلاتی' } as const

export default async function globalSetup() {
  const { api, csrf } = await adminApi()
  const headers = { 'X-CSRF-Token': csrf }
  const post = async (path: string, data: object, key = false) => {
    const res = await api.post(path, { data, headers: key ? { ...headers, 'Idempotency-Key': crypto.randomUUID() } : headers })
    expect(res.status(), `POST ${path}: ${await res.text()}`).toBe(201)
    return res.json()
  }
  const existing = await (await api.get('/api/v1/admin/products', { headers })).json()
  if ((existing.items ?? []).length > 0) return // already seeded (re-run against the same database)
  const drinks = await post('/api/v1/admin/categories', { name: 'نوشیدنی', sort_order: 1 })
  const snacks = await post('/api/v1/admin/categories', { name: 'کیک و اسنک', sort_order: 2 })
  await post('/api/v1/admin/products', { category_id: drinks.id, name: PRODUCTS.espresso, price_rial: 950000, inventory_mode: 'UNTRACKED' })
  const cake = await post('/api/v1/admin/products', { category_id: snacks.id, name: PRODUCTS.cake, price_rial: 1200000, inventory_mode: 'TRACKED' })
  const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Tehran' }).format(new Date())
  await post('/api/v1/admin/purchases', { product_id: cake.id, quantity: 20, unit_cost_rial: 400000, purchased_on: today }, true)
  await api.dispose()
}

import { readFileSync } from 'node:fs'
import { inflateRawSync } from 'node:zlib'

import { expect, request, test, type Page } from '@playwright/test'

import { adminApi, baseURL, env, newAccount, staffLoginUI } from './helpers'

// C6 §3C/§3E: manager customer views, reports and real downloads; ADMIN versus OPERATOR.
test.describe.configure({ mode: 'serial' })

const C = newAccount('سیما')
// A formula-like last name must reach spreadsheets as text.
const HOSTILE_LAST = '=HYPERLINK("http://x")'
const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Tehran' }).format(new Date())
const range = `?from=${today}&to=${today}`

test.beforeAll(async () => {
  // A customer with one completed, paid order, created through the API like the app does.
  const cust = await request.newContext({ baseURL: baseURL(), extraHTTPHeaders: { Origin: baseURL() } })
  const reg = await cust.post('/api/v1/customer/auth/register', {
    data: { first_name: C.first, last_name: HOSTILE_LAST, phone: C.phone, password: C.password },
  })
  expect(reg.status(), await reg.text()).toBe(201)
  const csrf = (await reg.json()).csrf_token as string
  const menu = await (await cust.get('/api/v1/public/menu')).json()
  const product = (menu.categories ?? []).flatMap((c: { products: { id: number; price_rial: number }[] }) => c.products)[0]
  const placed = await cust.post('/api/v1/public/orders', {
    data: { items: [{ product_id: product.id, quantity: 1, expected_unit_price_rial: product.price_rial }] },
    headers: { 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID(), 'X-Client-Id': crypto.randomUUID() },
  })
  expect(placed.status(), await placed.text()).toBe(201)
  const number = (await placed.json()).order_number
  const { api, csrf: staff } = await adminApi()
  const list = await (await api.get('/api/v1/admin/orders?status=PENDING', { headers: { 'X-CSRF-Token': staff } })).json()
  const id = list.items.filter((o: { order_number: number }) => o.order_number === number).at(-1).id
  for (const step of ['accept', 'ready', 'complete']) {
    expect((await api.post(`/api/v1/admin/orders/${id}/${step}`, { headers: { 'X-CSRF-Token': staff } })).ok()).toBe(true)
  }
  const pay = await api.post(`/api/v1/admin/orders/${id}/payments`, {
    data: { method: 'CASH', amount_rial: product.price_rial },
    headers: { 'X-CSRF-Token': staff, 'Idempotency-Key': crypto.randomUUID() },
  })
  expect(pay.status()).toBe(201)
  await api.dispose()
  await cust.dispose()
})

// One signed-in admin page for the whole file: staff logins are limited to 5 per 15 minutes per
// username (SECURITY.md), counting successful ones.
let admin: Page
test.beforeAll(async ({ browser }) => {
  admin = await (await browser.newContext()).newPage()
  await staffLoginUI(admin, env('E2E_ADMIN_USER'), env('E2E_ADMIN_PASSWORD'))
})
test.afterAll(async () => admin.context().close())

test('ADMIN: customer list, search and detail with ledger totals', async () => {
  const page = admin
  await page.goto('/admin/')
  await page.getByRole('link', { name: 'مشتریان' }).first().click()
  await expect(page).toHaveURL(/\/admin\/customers$/)
  await page.getByRole('searchbox').fill(C.phone.slice(-6))
  const row = page.getByRole('link', { name: new RegExp(C.first) })
  await expect(row).toBeVisible()
  await row.click()
  await expect(page).toHaveURL(/\/admin\/customers\/\d+$/)
  for (const label of ['پرداخت دریافتی (ناخالص)', 'بازپرداخت ثبت‌شده', 'خالص دریافتی', 'مطالبات (اکنون)']) {
    await expect(page.getByText(label, { exact: true }).first()).toBeVisible()
  }
  await expect(page.getByText('تأییدنشده')).toBeVisible()
})

/** Minimal ZIP reader (central directory: the writer may stream entries with data descriptors). */
function unzip(file: string): Map<string, string> {
  const buf = readFileSync(file)
  let end = buf.length - 22
  while (end >= 0 && buf.readUInt32LE(end) !== 0x06054b50) end--
  expect(end, 'end of central directory').toBeGreaterThanOrEqual(0)
  const count = buf.readUInt16LE(end + 10)
  let p = buf.readUInt32LE(end + 16)
  const out = new Map<string, string>()
  for (let n = 0; n < count; n++) {
    expect(buf.readUInt32LE(p), 'central directory entry').toBe(0x02014b50)
    const method = buf.readUInt16LE(p + 10)
    const size = buf.readUInt32LE(p + 20)
    const nameLen = buf.readUInt16LE(p + 28)
    const extraLen = buf.readUInt16LE(p + 30)
    const commentLen = buf.readUInt16LE(p + 32)
    const local = buf.readUInt32LE(p + 42)
    const name = buf.subarray(p + 46, p + 46 + nameLen).toString()
    const start = local + 30 + buf.readUInt16LE(local + 26) + buf.readUInt16LE(local + 28)
    const data = buf.subarray(start, start + size)
    out.set(name, (method === 8 ? inflateRawSync(data) : data).toString('utf8'))
    p += 46 + nameLen + extraLen + commentLen
  }
  return out
}

test('ADMIN: customer report with real XLSX and CSV downloads', async () => {
  const page = admin
  await page.goto('/admin/customers/report')
  await expect(page.getByRole('heading', { name: 'گزارش خرید مشتریان' })).toBeVisible()
  await expect(page.getByRole('link', { name: new RegExp(C.first) })).toBeVisible()

  const [xlsx] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: /XLSX/ }).first().click()])
  expect(xlsx.suggestedFilename()).toMatch(/\.xlsx$/)
  const parts = unzip(await xlsx.path())
  const sheet = [...parts.entries()].find(([n]) => n.startsWith('xl/worksheets/'))?.[1] ?? ''
  expect(sheet).toContain('rightToLeft="1"')
  expect(sheet).toContain(C.phone)
  expect(sheet).not.toContain('<f>') // no formula cells: the hostile name is text
  expect(parts.get('xl/styles.xml') ?? '').toContain('quotePrefix')

  const [csv] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: /CSV/ }).first().click()])
  expect(csv.suggestedFilename()).toMatch(/\.csv$/)
  const raw = readFileSync(await csv.path())
  expect([...raw.subarray(0, 3)]).toEqual([0xef, 0xbb, 0xbf])
  const text = raw.toString('utf8')
  expect(text).toContain(C.phone)
  expect(text).toContain(`'${HOSTILE_LAST}`.replaceAll('"', '""'))

  for (const path of [`/api/v1/admin/reports/customers.csv${range}`, `/api/v1/admin/reports/customers.xlsx${range}`, '/api/v1/admin/customers']) {
    const res = await page.request.get(path)
    expect(res.status(), path).toBe(200)
    expect(res.headers()['cache-control'], path).toBe('no-store')
  }
})

test('OPERATOR: no customer navigation, page or data (403), order queue still works', async ({ page }) => {
  await staffLoginUI(page, env('E2E_OPERATOR_USER'), env('E2E_OPERATOR_PASSWORD'))
  await expect(page.getByRole('link', { name: 'مشتریان' })).toHaveCount(0)
  await page.goto('/admin/customers')
  await expect(page.getByText('دسترسی ندارید')).toBeVisible()
  for (const path of ['/api/v1/admin/customers', `/api/v1/admin/reports/customers${range}`, `/api/v1/admin/reports/customers.csv${range}`, `/api/v1/admin/reports/customers.xlsx${range}`]) {
    expect((await page.request.get(path)).status(), path).toBe(403)
  }
  expect((await page.request.get('/api/v1/admin/orders')).status()).toBe(200)
})

test('staff and customer sessions never cross', async () => {
  const page = admin
  expect((await page.request.get('/api/v1/customer/auth/me')).status()).toBe(401)
  expect((await page.request.get('/api/v1/customer/orders')).status()).toBe(401)
})

import { expect, test, type Page } from '@playwright/test'

import { PRODUCTS } from './global-setup'
import { addToCart, checkout, loginUI, newAccount, registerUI } from './helpers'

// C6 §3B/§3E: the customer journey in a real browser against the isolated backend.
test.describe.configure({ mode: 'serial' })

const A = newAccount('آرش')
const B = newAccount('بهار')
let orderA = 0

const myOrderLinks = (page: Page) => page.locator('a[href^="/menu/my-orders/"]')

test('registration, signed-in checkout with the customer CSRF token, history and detail', async ({ page }) => {
  await registerUI(page, A)
  await expect(page.getByText('تأییدنشده').or(page.getByText('تأیید نشده')).first()).toBeVisible() // phone never claimed verified
  await addToCart(page, PRODUCTS.espresso, 2)
  await page.goto('/menu/cart')
  await expect(page.getByText(`این سفارش در «سفارش‌های من» حساب ${A.first} ثبت می‌شود.`)).toBeVisible()
  const sent = page.waitForRequest((r) => r.url().endsWith('/api/v1/public/orders') && r.method() === 'POST')
  await checkout(page)
  const headers = (await sent).headers()
  expect(headers['x-csrf-token'], 'customer CSRF token on the order').toBeTruthy()
  expect(headers['idempotency-key']).toMatch(/^[0-9a-f-]{36}$/)

  await page.goto('/menu/my-orders')
  await expect(myOrderLinks(page)).toHaveCount(1)
  await myOrderLinks(page).first().click()
  await expect(page).toHaveURL(/\/menu\/my-orders\/\d+$/)
  orderA = Number(page.url().split('/').pop())
  await expect(page.getByText(PRODUCTS.espresso)).toBeVisible()
  await expect(page.getByText('اقلام (با قیمت زمان سفارش)')).toBeVisible()
  await expect(page.getByText(/۲ ×/)).toBeVisible() // Persian digits

  const res = await page.request.get('/api/v1/customer/orders')
  expect(res.headers()['cache-control']).toBe('no-store')
})

test('customer B cannot see A’s order (UI and API) and has an empty history', async ({ page }) => {
  await registerUI(page, B)
  await page.goto(`/menu/my-orders/${orderA}`)
  await expect(page.getByText('این سفارش پیدا نشد')).toBeVisible()
  expect((await page.request.get(`/api/v1/customer/orders/${orderA}`)).status()).toBe(404)
  await page.goto('/menu/my-orders')
  await expect(page.getByText('هنوز سفارشی با این حساب ثبت نشده است')).toBeVisible()
  // A guest-style order without the customer CSRF token is refused while the cookie is present.
  const res = await page.request.post('/api/v1/public/orders', {
    data: { items: [] },
    headers: { Origin: new URL(page.url()).origin, 'Idempotency-Key': crypto.randomUUID() },
  })
  expect(res.status()).toBe(403)
})

for (const failure of ['network', '503'] as const) {
  test(`a ${failure} failure of /customer/auth/me never turns A into a guest; retry recovers and checkout works`, async ({ page }) => {
    await loginUI(page, A)
    await addToCart(page, PRODUCTS.cake)
    await page.route('**/api/v1/customer/auth/me', (route) =>
      failure === 'network'
        ? route.abort('failed')
        : route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":{"code":"UNAVAILABLE","message":"x"}}' }),
    )
    await page.goto('/menu/cart')
    const alert = page.getByRole('alert').filter({ hasText: 'وضعیت ورود به حساب شما بررسی نشد' })
    await expect(alert).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('button', { name: 'ثبت سفارش' })).toBeDisabled()
    await expect(page.getByText('به‌صورت مهمان سفارش می‌دهید')).toHaveCount(0)
    await expect(page.locator('header a[href="/menu/login"]')).toHaveCount(0)
    // Private pages wait instead of redirecting to the login page.
    await page.goto('/menu/my-orders')
    await expect(page.getByText('وضعیت ورود به حساب شما بررسی نشد', { exact: false })).toBeVisible({ timeout: 20_000 })
    await expect(page).toHaveURL(/\/menu\/my-orders$/)

    await page.goto('/menu/cart')
    await expect(alert).toBeVisible({ timeout: 20_000 })
    await page.unroute('**/api/v1/customer/auth/me')
    await alert.getByRole('button', { name: 'تلاش دوباره' }).click()
    await expect(page.getByText(`این سفارش در «سفارش‌های من» حساب ${A.first} ثبت می‌شود.`)).toBeVisible()
    await checkout(page)
    await page.goto('/menu/my-orders')
    await expect(myOrderLinks(page)).toHaveCount(failure === 'network' ? 2 : 3)
  })
}

test('logout returns to the menu as a guest; the session is gone', async ({ page }) => {
  await loginUI(page, A)
  await page.getByRole('button', { name: 'خروج از حساب' }).click()
  await expect(page).toHaveURL(/\/menu$/)
  await expect(page.locator('header a[href="/menu/login"]')).toBeVisible()
  expect((await page.request.get('/api/v1/customer/auth/me')).status()).toBe(401)
  await page.goto('/menu/my-orders')
  await expect(page).toHaveURL(/\/menu\/login$/)
})

test('guest ordering still works and is not attributed', async ({ page }) => {
  await addToCart(page, PRODUCTS.espresso)
  await page.goto('/menu/cart')
  await expect(page.getByText('به‌صورت مهمان سفارش می‌دهید')).toBeVisible()
  const sent = page.waitForRequest((r) => r.url().endsWith('/api/v1/public/orders') && r.method() === 'POST')
  await checkout(page)
  expect((await sent).headers()['x-csrf-token']).toBeUndefined()
  await expect(page.getByText(/سفارش/).first()).toBeVisible()
})

test('password change ends the other sessions of the account', async ({ browser }) => {
  // Its own account: login attempts per phone are rate limited (5 per 15 minutes), and A has
  // already signed in several times in this file.
  const P = newAccount('پویا')
  const one = await browser.newContext()
  const two = await browser.newContext()
  const p1 = await one.newPage()
  const p2 = await two.newPage()
  await registerUI(p1, P)
  await loginUI(p2, P)
  const next = P.password + '-2'
  await p1.getByLabel('رمز فعلی').fill(P.password)
  await p1.getByLabel('رمز جدید', { exact: true }).fill(next)
  await p1.getByLabel('تکرار رمز جدید').fill(next)
  const changed = p1.waitForResponse((r) => r.url().endsWith('/api/v1/customer/auth/password'))
  await p1.getByRole('button', { name: 'تغییر رمز' }).click()
  expect((await changed).status()).toBe(200)
  await expect(p1.getByText('رمز عبور تغییر کرد و همهٔ نشست‌های دیگر حساب بسته شد.')).toBeVisible()
  P.password = next
  expect((await p1.request.get('/api/v1/customer/auth/me')).status()).toBe(200)
  expect((await p2.request.get('/api/v1/customer/auth/me')).status()).toBe(401)
  await one.close()
  await two.close()
})

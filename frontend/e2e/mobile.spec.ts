import { expect, test } from '@playwright/test'

import { PRODUCTS } from './global-setup'
import { addToCart, env, expectNoHorizontalScroll, newAccount, registerUI, staffLoginUI } from './helpers'

// C6 §3E–F on a phone viewport: Persian RTL layout, Persian digits, no horizontal scroll, and the
// staff PWA never controlling or caching customer pages.
test.describe.configure({ mode: 'serial' })

test('customer pages are Persian, RTL and fit a phone screen', async ({ page }) => {
  for (const path of ['/menu', '/menu/login', '/menu/register']) {
    await page.goto(path)
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl')
    await expect(page.locator('html')).toHaveAttribute('lang', /^fa/)
    await expectNoHorizontalScroll(page)
  }
  await page.goto('/menu')
  const price = page.getByText(/تومان/).first()
  await expect(price).toBeVisible()
  expect(await price.textContent()).not.toMatch(/[0-9]/) // prices in Persian digits

  const D = newAccount('دنیا')
  await registerUI(page, D)
  await expectNoHorizontalScroll(page)
  await addToCart(page, PRODUCTS.cake)
  await page.goto('/menu/cart')
  await expect(page.getByRole('button', { name: 'ثبت سفارش' })).toBeVisible()
  await expectNoHorizontalScroll(page)
  await page.goto('/menu/my-orders')
  await expectNoHorizontalScroll(page)
  // Touch targets of the header stay at least 44 px.
  const account = page.locator('header a[href="/menu/account"]')
  const box = await account.boundingBox()
  expect(box?.height ?? 0).toBeGreaterThanOrEqual(44)
})

test('the customer app registers no service worker and is never controlled by one', async ({ page }) => {
  await page.goto('/menu')
  await page.waitForLoadState('networkidle')
  const sw = await page.evaluate(async () => ({
    registrations: (await navigator.serviceWorker.getRegistrations()).map((r) => r.scope),
    controlled: navigator.serviceWorker.controller !== null,
    manifest: document.querySelector('link[rel="manifest"]')?.getAttribute('href') ?? null,
  }))
  expect(sw).toEqual({ registrations: [], controlled: false, manifest: null })
  const me = await page.request.get('/api/v1/customer/auth/me')
  expect(me.headers()['cache-control']).toBe('no-store')
})

test('the staff PWA is scoped to /admin/ and does not control /menu', async ({ page }) => {
  await staffLoginUI(page, env('E2E_ADMIN_USER'), env('E2E_ADMIN_PASSWORD'))
  await expect.poll(async () => page.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).map((r) => new URL(r.scope).pathname))).toEqual(['/admin/'])
  await page.reload()
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.scriptURL ?? null)).toMatch(/\/admin\/sw\.js$/)
  await page.goto('/menu')
  expect(await page.evaluate(() => navigator.serviceWorker.controller)).toBeNull()
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl')
  await expectNoHorizontalScroll(page)
})

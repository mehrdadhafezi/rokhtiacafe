import { expect, request, type APIRequestContext, type Page } from '@playwright/test'

/** Credentials and URL of the isolated backend (scripts/e2e.sh sets them; never real accounts). */
export function env(name: string): string {
  const v = process.env[name]
  if (!v) throw new Error(`${name} is not set: run scripts/e2e.sh`)
  return v
}

export const baseURL = () => env('E2E_BASE_URL')

/** A staff API session (Origin + CSRF exactly like the staff app). */
export async function staffApi(user: string, password: string): Promise<{ api: APIRequestContext; csrf: string }> {
  const api = await request.newContext({ baseURL: baseURL(), extraHTTPHeaders: { Origin: baseURL() } })
  const res = await api.post('/api/v1/auth/login', { data: { username: user, password } })
  expect(res.status(), 'staff login').toBe(200)
  const csrf = (await res.json()).csrf_token as string
  return { api, csrf }
}

export const adminApi = () => staffApi(env('E2E_ADMIN_USER'), env('E2E_ADMIN_PASSWORD'))

/** A fresh Iranian mobile number for this run. */
export function uniquePhone(): string {
  return '0912' + String(Math.floor(Math.random() * 1e7)).padStart(7, '0')
}

export const customerPassword = () => 'e2e-' + Math.random().toString(36).slice(2) + '-pass'

export interface Account {
  first: string
  phone: string
  password: string
}

export function newAccount(first: string): Account {
  return { first, phone: uniquePhone(), password: customerPassword() }
}

/** Registration through the UI; ends on /menu/account. */
export async function registerUI(page: Page, a: Account) {
  await page.goto('/menu/register')
  await page.getByLabel('نام', { exact: true }).fill(a.first)
  await page.getByLabel('نام خانوادگی').fill('آزمون')
  await page.getByLabel('شمارهٔ موبایل').fill(a.phone)
  await page.getByLabel('رمز عبور', { exact: true }).fill(a.password)
  await page.getByLabel('تکرار رمز عبور').fill(a.password)
  await page.getByRole('button', { name: 'ساخت حساب' }).click()
  await expect(page).toHaveURL(/\/menu\/account$/)
  await expect(page.getByText(a.first).first()).toBeVisible()
}

export async function loginUI(page: Page, a: Account) {
  await page.goto('/menu/login')
  await page.getByLabel('شمارهٔ موبایل').fill(a.phone)
  await page.getByLabel('رمز عبور').fill(a.password)
  await page.getByRole('button', { name: 'ورود', exact: true }).click()
  await expect(page).toHaveURL(/\/menu\/account$/)
}

export async function addToCart(page: Page, product: string, times = 1) {
  await page.goto('/menu')
  // The first tap adds the product; the card then shows a quantity stepper.
  await page.getByRole('button', { name: `افزودن ${product} به سبد` }).click()
  for (let i = 1; i < times; i++) await page.getByRole('button', { name: `افزودن یک ${product}` }).first().click()
}

/** Checkout from /menu/cart; returns the tracking path /menu/order#token. */
export async function checkout(page: Page): Promise<void> {
  await page.goto('/menu/cart')
  const submit = page.getByRole('button', { name: 'ثبت سفارش' })
  await expect(submit).toBeEnabled()
  const created = page.waitForResponse((r) => r.url().endsWith('/api/v1/public/orders') && r.request().method() === 'POST')
  await submit.click()
  expect((await created).status()).toBe(201)
  await expect(page).toHaveURL(/\/menu\/order#/)
}

/** The staff login is a gate on every /admin page (there is no /admin/login route). */
export async function staffLoginUI(page: Page, user: string, password: string) {
  await page.goto('/admin/')
  await expect(page.getByRole('heading', { name: 'ورود کارکنان' })).toBeVisible()
  await page.getByLabel('نام کاربری').fill(user)
  await page.getByLabel('رمز عبور').fill(password)
  await page.getByRole('button', { name: 'ورود', exact: true }).click()
  await expect(page.getByRole('button', { name: 'خروج', exact: true })).toBeVisible() // navigation folds away on phones
}

/** No horizontal page scroll (mobile layout rule). */
export async function expectNoHorizontalScroll(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow, 'horizontal overflow in px').toBeLessThanOrEqual(1)
}

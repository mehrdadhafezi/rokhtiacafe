import { useMutation } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Link, Navigate, useLocation, useNavigate } from 'react-router'

import { Field, FormError, inputClass, Notice } from '@/components/form'
import { LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { isApiError } from '@/lib/api'
import { faDigits } from '@/lib/digits'
import { useZodForm } from '@/lib/form'

import { customerApi } from './api'
import { SessionCheckFailed } from './SessionCheckFailed'
import { useCustomerSession } from './session-context'
import { loginSchema, MIN_PASSWORD, registerSchema, safeReturnPath } from './validation'

function AuthShell({ title, description, children, footer }: { title: string; description: string; children: ReactNode; footer: ReactNode }) {
  return (
    <div className="mx-auto max-w-sm space-y-4">
      <div>
        <h1 className="text-2xl font-extrabold">{title}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{description}</p>
      </div>
      <div className="rounded-xl border border-border/70 bg-surface p-4">{children}</div>
      <p className="text-center text-sm">{footer}</p>
      <p className="text-center text-sm">
        <Link to="/menu" className="text-primary underline-offset-4 hover:underline">
          بازگشت به منو (سفارش بدون حساب هم ممکن است)
        </Link>
      </p>
    </div>
  )
}

/** Where to go after signing in: the protected page that sent the visitor here, or the account. */
function useReturnPath(): string {
  const location = useLocation()
  return safeReturnPath((location.state as { from?: unknown } | null)?.from)
}

/** /menu/login — mobile + password. The cart is kept: it lives in local storage. */
export function LoginPage() {
  const { state, signedIn } = useCustomerSession()
  const navigate = useNavigate()
  const next = useReturnPath()
  const form = useZodForm(loginSchema, { defaultValues: { phone: '', password: '' } })
  const login = useMutation({
    mutationFn: (v: { phone: string; password: string }) => customerApi.login(v.phone, v.password),
    onSuccess: (s) => {
      signedIn(s)
      navigate(next, { replace: true })
    },
  })
  if (state.status === 'loading') return <LoadingState />
  if (state.status === 'customer') return <Navigate to={next} replace />

  const errors = form.formState.errors
  const wrong = isApiError(login.error, 'UNAUTHENTICATED')
  return (
    <AuthShell
      title="ورود به حساب"
      description="با شمارهٔ موبایل و رمز عبور وارد شوید تا سابقهٔ سفارش‌هایتان را ببینید."
      footer={
        <>
          حساب ندارید؟{' '}
          <Link to="/menu/register" state={{ from: next }} className="font-medium text-primary underline-offset-4 hover:underline">
            ثبت‌نام کنید
          </Link>
        </>
      }
    >
      <form onSubmit={form.handleSubmit((v) => login.mutate(v))} className="space-y-4" noValidate>
        <Field label="شمارهٔ موبایل" error={errors.phone?.message}>
          {(p) => (
            <input
              {...p}
              {...form.register('phone')}
              dir="ltr"
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              placeholder="09123456789"
              className={inputClass}
            />
          )}
        </Field>
        <Field label="رمز عبور" error={errors.password?.message}>
          {(p) => <input {...p} {...form.register('password')} dir="ltr" type="password" autoComplete="current-password" className={inputClass} />}
        </Field>
        <FormError error={login.error} message={wrong ? 'شمارهٔ موبایل یا رمز عبور اشتباه است.' : undefined} />
        <Button type="submit" size="lg" className="w-full" disabled={login.isPending}>
          {login.isPending ? 'در حال ورود…' : 'ورود'}
        </Button>
      </form>
    </AuthShell>
  )
}

/** /menu/register — first/last name, mobile, password. The mobile number is NOT verified. */
export function RegisterPage() {
  const { state, signedIn } = useCustomerSession()
  const navigate = useNavigate()
  const next = useReturnPath()
  const form = useZodForm(registerSchema, { defaultValues: { first_name: '', last_name: '', phone: '', password: '', repeat: '' } })
  const register = useMutation({
    mutationFn: (v: { first_name: string; last_name: string; phone: string; password: string }) =>
      customerApi.register({ first_name: v.first_name, last_name: v.last_name, phone: v.phone, password: v.password }),
    onSuccess: (s) => {
      signedIn(s)
      navigate(next, { replace: true })
    },
  })
  if (state.status === 'loading') return <LoadingState />
  if (state.status === 'customer') return <Navigate to={next} replace />

  const errors = form.formState.errors
  const conflict = isApiError(register.error, 'CONFLICT')
  return (
    <AuthShell
      title="ساخت حساب مشتری"
      description="با حساب، سفارش‌هایتان در «سفارش‌های من» نگه داشته می‌شود."
      footer={
        <>
          حساب دارید؟{' '}
          <Link to="/menu/login" state={{ from: next }} className="font-medium text-primary underline-offset-4 hover:underline">
            وارد شوید
          </Link>
        </>
      }
    >
      <form onSubmit={form.handleSubmit((v) => register.mutate(v))} className="space-y-4" noValidate>
        <div className="grid grid-cols-2 gap-3">
          <Field label="نام" error={errors.first_name?.message}>
            {(p) => <input {...p} {...form.register('first_name')} autoComplete="given-name" className={inputClass} />}
          </Field>
          <Field label="نام خانوادگی" error={errors.last_name?.message}>
            {(p) => <input {...p} {...form.register('last_name')} autoComplete="family-name" className={inputClass} />}
          </Field>
        </div>
        <Field label="شمارهٔ موبایل" error={errors.phone?.message} hint="این شماره فعلاً با پیامک تأیید نمی‌شود.">
          {(p) => (
            <input
              {...p}
              {...form.register('phone')}
              dir="ltr"
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              placeholder="09123456789"
              className={inputClass}
            />
          )}
        </Field>
        <Field label="رمز عبور" error={errors.password?.message} hint={`دست‌کم ${faDigits(MIN_PASSWORD)} نویسه.`}>
          {(p) => <input {...p} {...form.register('password')} dir="ltr" type="password" autoComplete="new-password" className={inputClass} />}
        </Field>
        <Field label="تکرار رمز عبور" error={errors.repeat?.message}>
          {(p) => <input {...p} {...form.register('repeat')} dir="ltr" type="password" autoComplete="new-password" className={inputClass} />}
        </Field>
        <Notice>
          نام و شمارهٔ موبایل شما برای نمایش سابقهٔ سفارش‌ها و گزارش‌های مدیریت کافه نگه داشته می‌شود. رمز عبور به‌صورت رمزنگاری‌شده ذخیره می‌شود و
          هیچ‌کس آن را نمی‌بیند.
        </Notice>
        <FormError
          error={register.error}
          message={conflict ? 'ثبت‌نام با این شماره ممکن نیست. اگر قبلاً حساب ساخته‌اید، وارد شوید.' : undefined}
        />
        <Button type="submit" size="lg" className="w-full" disabled={register.isPending}>
          {register.isPending ? 'در حال ثبت…' : 'ساخت حساب'}
        </Button>
      </form>
    </AuthShell>
  )
}

/** Protects /menu/account and /menu/my-orders: guests are sent to the login page and back. */
export function RequireCustomer({ children }: { children: ReactNode }) {
  const { state } = useCustomerSession()
  const location = useLocation()
  if (state.status === 'loading') return <LoadingState />
  // Unknown identity: never send a possibly signed-in customer to the login page.
  if (state.status === 'error') return <SessionCheckFailed what="پس از برقراری اتصال، این صفحه نمایش داده می‌شود." />
  if (state.status === 'guest') return <Navigate to="/menu/login" replace state={{ from: location.pathname }} />
  return children
}

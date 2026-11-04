import { useInfiniteQuery, useMutation, useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router'

import { Field, FormError, inputClass, Notice } from '@/components/form'
import { ChevronIcon } from '@/components/icons'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { isApiError } from '@/lib/api'
import { formatDate, formatDateTime } from '@/lib/date'
import { faDigits, toPersianDigits } from '@/lib/digits'
import { useZodForm } from '@/lib/form'
import { formatToman } from '@/lib/money'
import { cn } from '@/lib/utils'

import { activityKey, customerApi, historyKey, historyOrderKey, type HistoryOrder } from './api'
import { activityLabel, historyPaymentText, historyStatus, paymentKindLabel, paymentMethodLabel } from './labels'
import { useCustomerSession } from './session-context'
import { MIN_PASSWORD, passwordSchema } from './validation'

const toneClass = {
  neutral: 'bg-muted text-foreground',
  primary: 'bg-primary/10 text-primary',
  success: 'bg-success/10 text-success',
  danger: 'bg-danger/10 text-danger',
}

function StatusBadge({ order }: { order: Pick<HistoryOrder, 'status' | 'expired'> }) {
  const s = historyStatus(order)
  return <span className={cn('inline-flex rounded-full px-2.5 py-0.5 text-sm font-medium whitespace-nowrap', toneClass[s.tone])}>{s.label}</span>
}

function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link to={to} aria-label={label} className="grid size-11 place-items-center rounded-full hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50">
      <ChevronIcon />
    </Link>
  )
}

/** /menu/account — profile, password, sign-out and recent account activity. */
export function AccountPage() {
  const { state, csrf, signedIn, signedOut } = useCustomerSession()
  const navigate = useNavigate()
  const form = useZodForm(passwordSchema, { defaultValues: { current_password: '', new_password: '', repeat: '' } })
  const activity = useQuery({ queryKey: activityKey, queryFn: ({ signal }) => customerApi.activity(signal) })
  const logout = useMutation({
    mutationFn: () => customerApi.logout(csrf),
    onSettled: (_d, err) => {
      // Signed out locally even if the session had already expired on the server.
      if (!err || isApiError(err, 'UNAUTHENTICATED') || isApiError(err, 'CSRF_FAILED')) {
        // Leave the protected page first (navigation is asynchronous); otherwise its guard sends
        // the visitor to the login page.
        void Promise.resolve(navigate('/menu', { replace: true })).then(signedOut)
      }
    },
  })
  const change = useMutation({
    mutationFn: (v: { current_password: string; new_password: string }) => customerApi.changePassword(csrf, v.current_password, v.new_password),
    onSuccess: (s) => {
      signedIn(s) // the server rotated the session and its CSRF token
      form.reset()
    },
  })
  if (state.status !== 'customer') return <LoadingState />
  const c = state.customer
  const wrongCurrent =
    isApiError(change.error, 'VALIDATION_ERROR') && Boolean((change.error.details.fields as Record<string, string> | undefined)?.current_password)
  const errors = form.formState.errors

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <BackLink to="/menu" label="بازگشت به منو" />
        <h1 className="text-xl font-extrabold">حساب من</h1>
      </div>

      <section className="rounded-xl border border-border/70 bg-surface p-4">
        <p className="text-lg font-bold">
          {c.first_name} {c.last_name}
        </p>
        <p className="mt-1 text-sm text-muted-foreground">
          <span dir="ltr" className="tabular-nums">
            {toPersianDigits(c.phone)}
          </span>
          <span className="ms-2 rounded-full bg-muted px-2 py-0.5 text-xs">تأییدنشده</span>
        </p>
        <div className="mt-4 flex flex-wrap gap-2">
          <Button asChild>
            <Link to="/menu/my-orders">سفارش‌های من</Link>
          </Button>
          <Button variant="outline" onClick={() => logout.mutate()} disabled={logout.isPending}>
            {logout.isPending ? 'در حال خروج…' : 'خروج از حساب'}
          </Button>
        </div>
        <FormError error={logout.error && !isApiError(logout.error, 'UNAUTHENTICATED') ? logout.error : null} />
      </section>

      <section className="rounded-xl border border-border/70 bg-surface p-4">
        <h2 className="mb-3 font-bold">تغییر رمز عبور</h2>
        <form onSubmit={form.handleSubmit((v) => change.mutate(v))} className="space-y-4" noValidate>
          <Field label="رمز فعلی" error={errors.current_password?.message}>
            {(p) => <input {...p} {...form.register('current_password')} dir="ltr" type="password" autoComplete="current-password" className={inputClass} />}
          </Field>
          <Field label="رمز جدید" error={errors.new_password?.message} hint={`دست‌کم ${faDigits(MIN_PASSWORD)} نویسه.`}>
            {(p) => <input {...p} {...form.register('new_password')} dir="ltr" type="password" autoComplete="new-password" className={inputClass} />}
          </Field>
          <Field label="تکرار رمز جدید" error={errors.repeat?.message}>
            {(p) => <input {...p} {...form.register('repeat')} dir="ltr" type="password" autoComplete="new-password" className={inputClass} />}
          </Field>
          {change.isSuccess && <Notice tone="success">رمز عبور تغییر کرد و همهٔ نشست‌های دیگر حساب بسته شد.</Notice>}
          <FormError error={change.error} message={wrongCurrent ? 'رمز فعلی اشتباه است.' : undefined} />
          <Button type="submit" disabled={change.isPending}>
            {change.isPending ? 'در حال ثبت…' : 'تغییر رمز'}
          </Button>
        </form>
      </section>

      <section className="rounded-xl border border-border/70 bg-surface p-4">
        <h2 className="mb-2 font-bold">فعالیت‌های اخیر حساب</h2>
        {activity.isPending ? (
          <LoadingState />
        ) : activity.isError ? (
          <ErrorState error={activity.error} onRetry={() => void activity.refetch()} />
        ) : activity.data.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">فعالیتی ثبت نشده است.</p>
        ) : (
          <ul className="divide-y">
            {activity.data.items.map((a) => (
              <li key={a.id} className="flex items-center justify-between gap-3 py-2 text-sm">
                <span>
                  {activityLabel[a.type] ?? a.type}
                  {a.order_id && a.order_number != null && (
                    <Link to={`/menu/my-orders/${a.order_id}`} className="ms-1 text-primary underline-offset-4 hover:underline">
                      (سفارش {faDigits(a.order_number)})
                    </Link>
                  )}
                </span>
                <time dateTime={a.created_at} className="text-muted-foreground tabular-nums">
                  {formatDateTime(a.created_at)}
                </time>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function OrderCard({ o }: { o: HistoryOrder }) {
  const pay = historyPaymentText(o)
  const count = o.items.reduce((n, i) => n + i.quantity, 0)
  return (
    <Link
      to={`/menu/my-orders/${o.id}`}
      className="block rounded-xl border border-border/70 bg-surface p-3 transition hover:border-primary/40 focus-visible:ring-[3px] focus-visible:ring-ring/50"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="font-bold">سفارش {faDigits(o.order_number)}</span>
        <StatusBadge order={o} />
      </div>
      <p className="mt-1 truncate text-sm text-muted-foreground">{o.items.map((i) => `${i.name} × ${faDigits(i.quantity)}`).join('، ')}</p>
      <div className="mt-2 flex items-center justify-between text-sm">
        <time dateTime={o.created_at} className="text-muted-foreground">
          {formatDateTime(o.created_at)}
        </time>
        <span className="tabular-nums">
          {faDigits(count)} قلم · <b>{formatToman(o.total_rial)}</b>
        </span>
      </div>
      {pay && <p className="mt-1 text-xs text-muted-foreground">{pay}</p>}
    </Link>
  )
}

/** /menu/my-orders — the customer's own orders, newest first, 20 at a time. */
export function MyOrdersPage() {
  const orders = useInfiniteQuery({
    queryKey: historyKey,
    queryFn: ({ pageParam, signal }) => customerApi.orders(pageParam, signal),
    initialPageParam: null as number | null,
    getNextPageParam: (last) => last.next_before_id,
  })
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <BackLink to="/menu/account" label="بازگشت به حساب" />
        <h1 className="text-xl font-extrabold">سفارش‌های من</h1>
      </div>
      {orders.isPending ? (
        <LoadingState />
      ) : orders.isError ? (
        <ErrorState error={orders.error} onRetry={() => void orders.refetch()} />
      ) : orders.data.pages[0]?.items.length === 0 ? (
        <EmptyState
          title="هنوز سفارشی با این حساب ثبت نشده است"
          description="سفارش‌هایی که پس از ورود ثبت کنید اینجا می‌آیند. سفارش‌های مهمانِ قبلی به حساب منتقل نمی‌شوند."
          action={
            <Button asChild>
              <Link to="/menu">مشاهدهٔ منو</Link>
            </Button>
          }
        />
      ) : (
        <>
          <ul className="space-y-3">
            {orders.data.pages.flatMap((p) => p.items).map((o) => (
              <li key={o.id}>
                <OrderCard o={o} />
              </li>
            ))}
          </ul>
          {orders.hasNextPage && (
            <Button variant="outline" className="w-full" onClick={() => void orders.fetchNextPage()} disabled={orders.isFetchingNextPage}>
              {orders.isFetchingNextPage ? 'در حال بارگذاری…' : 'سفارش‌های قدیمی‌تر'}
            </Button>
          )}
        </>
      )}
    </div>
  )
}

/** /menu/my-orders/:id — one of the customer's orders (another id answers 404). */
export function MyOrderPage() {
  const id = Number(useParams().id)
  const valid = Number.isSafeInteger(id) && id > 0
  const order = useQuery({ queryKey: historyOrderKey(id), queryFn: ({ signal }) => customerApi.order(id, signal), enabled: valid })
  const header = (
    <div className="flex items-center gap-2">
      <BackLink to="/menu/my-orders" label="بازگشت به سفارش‌های من" />
      <h1 className="text-xl font-extrabold">{order.data ? `سفارش ${faDigits(order.data.order_number)}` : 'جزئیات سفارش'}</h1>
    </div>
  )
  if (!valid || isApiError(order.error, 'NOT_FOUND')) {
    return (
      <div className="space-y-4">
        {header}
        <EmptyState title="این سفارش پیدا نشد" description="فقط سفارش‌هایی که با همین حساب ثبت شده‌اند نمایش داده می‌شوند." />
      </div>
    )
  }
  if (order.isPending) return <LoadingState />
  if (order.isError) return <ErrorState error={order.error} onRetry={() => void order.refetch()} />
  const o = order.data
  const pay = historyPaymentText(o)
  return (
    <div className="space-y-4">
      {header}
      <section className="space-y-2 rounded-xl border border-border/70 bg-surface p-4">
        <div className="flex items-center justify-between gap-2">
          <StatusBadge order={o} />
          <time dateTime={o.created_at} className="text-sm text-muted-foreground">
            {formatDateTime(o.created_at)}
          </time>
        </div>
        {o.completed_at && <p className="text-sm text-muted-foreground">تحویل: {formatDateTime(o.completed_at)}</p>}
        {o.cancelled_at && <p className="text-sm text-muted-foreground">لغو: {formatDateTime(o.cancelled_at)}</p>}
        {o.customer_label && <p className="text-sm">نام روی سفارش: {o.customer_label}</p>}
        {o.customer_note && <p className="text-sm text-muted-foreground">یادداشت: {o.customer_note}</p>}
      </section>

      <section className="rounded-xl border border-border/70 bg-surface p-4">
        <h2 className="mb-2 font-bold">اقلام (با قیمت زمان سفارش)</h2>
        <ul className="divide-y">
          {o.items.map((i, n) => (
            <li key={n} className="flex items-center justify-between gap-3 py-2">
              <span>
                {i.name}
                <span className="block text-xs text-muted-foreground tabular-nums">
                  {faDigits(i.quantity)} × {formatToman(i.unit_price_rial)}
                </span>
              </span>
              <span className="tabular-nums">{formatToman(i.line_total_rial)}</span>
            </li>
          ))}
        </ul>
        <div className="mt-2 flex items-center justify-between border-t pt-2 text-lg font-extrabold">
          <span>جمع</span>
          <span className="tabular-nums">{formatToman(o.total_rial)}</span>
        </div>
        {pay && <p className="mt-1 text-sm text-muted-foreground">{pay}</p>}
      </section>

      {o.payments && o.payments.length > 0 && (
        <section className="rounded-xl border border-border/70 bg-surface p-4">
          <h2 className="mb-2 font-bold">پرداخت‌ها</h2>
          <ul className="divide-y text-sm">
            {o.payments.map((p, n) => (
              <li key={n} className="flex items-center justify-between gap-3 py-2">
                <span>
                  {paymentKindLabel[p.kind]} · {paymentMethodLabel[p.method]}
                  <span className="block text-xs text-muted-foreground">{formatDate(p.created_at)}</span>
                </span>
                <span className={cn('tabular-nums', p.kind === 'REFUND' && 'text-danger')}>
                  {p.kind === 'REFUND' ? '−' : ''}
                  {formatToman(p.amount_rial)}
                </span>
              </li>
            ))}
          </ul>
          {o.refunded_rial > 0 && <p className="mt-2 text-xs text-muted-foreground">بازپرداخت ثبت شده است؛ مبلغ را از صندوق دریافت کنید.</p>}
        </section>
      )}
    </div>
  )
}

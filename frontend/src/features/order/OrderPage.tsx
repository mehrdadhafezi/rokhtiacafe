import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router'

import { Dialog } from '@/components/Dialog'
import { CheckIcon, ClockIcon } from '@/components/icons'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { forgetOrder } from '@/features/cart/storage'
import { cancelOrder, fetchOrder, orderQueryKey, type PublicOrder } from '@/features/menu/api'
import { isApiError } from '@/lib/api'
import { formatTime } from '@/lib/date'
import { faDigits } from '@/lib/digits'
import { errorMessageFa } from '@/lib/errors-fa'
import { formatToman } from '@/lib/money'
import { cn } from '@/lib/utils'

import { canCancel, cancelReasonText, paymentText, pollInterval, progressSteps, statusLabel, stepIndex } from './status'

function Progress({ order }: { order: PublicOrder }) {
  const current = stepIndex(order.status)
  return (
    <ol className="grid grid-cols-4 gap-1" aria-label="مراحل سفارش">
      {progressSteps.map((s, i) => {
        const done = i < current
        const now = i === current
        return (
          <li key={s} className="flex flex-col items-center gap-1 text-center" aria-current={now ? 'step' : undefined}>
            <span
              className={cn(
                'grid size-8 place-items-center rounded-full border-2 text-sm font-bold transition',
                done && 'border-primary bg-primary text-primary-foreground',
                now && 'border-primary bg-surface text-primary ring-4 ring-primary/15',
                !done && !now && 'border-border bg-surface text-muted-foreground',
              )}
            >
              {done ? <CheckIcon width={16} height={16} /> : faDigits(i + 1)}
            </span>
            <span className={cn('text-xs leading-tight', now ? 'font-bold text-primary' : 'text-muted-foreground')}>{statusLabel[s]}</span>
          </li>
        )
      })}
    </ol>
  )
}

/** /menu/order#<token> — live order status, polled every 5 s until final (PRD §5.1 C5–C6). */
export function OrderPage() {
  const { hash } = useLocation()
  const token = decodeURIComponent(hash.replace(/^#/, ''))
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState(false)

  const query = useQuery({
    queryKey: orderQueryKey(token),
    queryFn: ({ signal }) => fetchOrder(token, signal),
    enabled: token.length > 0,
    refetchInterval: (q) => pollInterval(q.state.data),
    refetchIntervalInBackground: false,
  })

  const cancel = useMutation({
    mutationFn: () => cancelOrder(token),
    onSuccess: (order) => {
      queryClient.setQueryData(orderQueryKey(token), order)
      setConfirming(false)
    },
  })

  useEffect(() => {
    if (isApiError(query.error, 'NOT_FOUND')) forgetOrder(token)
  }, [query.error, token])

  if (!token) {
    return (
      <EmptyState
        title="سفارشی برای پیگیری نیست"
        action={
          <Button asChild>
            <Link to="/menu">مشاهدهٔ منو</Link>
          </Button>
        }
      />
    )
  }
  if (query.isPending) return <LoadingState label="در حال دریافت وضعیت سفارش…" />
  if (isApiError(query.error, 'NOT_FOUND')) {
    return (
      <EmptyState
        title="سفارش پیدا نشد"
        description="لینک پیگیری نامعتبر است یا بیش از ۲۴ ساعت از ثبت سفارش گذشته است."
        action={
          <Button asChild>
            <Link to="/menu">بازگشت به منو</Link>
          </Button>
        }
      />
    )
  }
  if (!query.data) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />

  const order = query.data
  const cancelled = order.status === 'CANCELLED' || order.expired
  const reason = cancelReasonText(order)
  const payment = paymentText(order)

  return (
    <div className="space-y-4">
      <section
        className={cn(
          'rounded-2xl px-5 py-6 text-center shadow-md transition-colors',
          cancelled ? 'bg-muted text-foreground' : order.status === 'READY' ? 'bg-success text-white' : 'bg-primary text-primary-foreground',
        )}
        aria-live="polite"
      >
        <p className="text-sm opacity-85">شمارهٔ سفارش</p>
        <p className="mt-1 text-5xl font-black tabular-nums">{faDigits(order.order_number)}</p>
        <p className="mt-3 text-lg font-bold">{order.expired && order.status === 'PENDING' ? 'منقضی شد' : statusLabel[order.status]}</p>
        {order.status === 'READY' && <p className="mt-1 text-sm">سفارش شما آماده است؛ لطفاً از پیشخوان تحویل بگیرید.</p>}
        {order.customer_label && <p className="mt-1 text-sm opacity-85">به نام {order.customer_label}</p>}
      </section>

      {!cancelled && (
        <div className="rounded-xl border border-border/70 bg-surface p-4">
          <Progress order={order} />
        </div>
      )}

      {order.status === 'PENDING' && !order.expired && (
        <p className="flex items-center gap-2 rounded-lg bg-warning/10 px-3 py-2 text-sm text-warning">
          <ClockIcon width={18} height={18} />
          اگر سفارش تا ساعت <b className="tabular-nums">{formatTime(order.expires_at)}</b> تأیید نشود، خودکار لغو می‌شود.
        </p>
      )}
      {reason && <p className="rounded-lg bg-muted px-3 py-2 text-sm">{reason}</p>}

      <section className="rounded-xl border border-border/70 bg-surface p-4">
        <h2 className="mb-3 font-bold">جزئیات سفارش</h2>
        <ul className="divide-y divide-border/60">
          {order.items.map((i) => (
            <li key={i.product_id} className="flex justify-between gap-3 py-2">
              <span>
                {i.name} <span className="text-muted-foreground tabular-nums">× {faDigits(i.quantity)}</span>
              </span>
              <span className="tabular-nums">{formatToman(i.line_total_rial)}</span>
            </li>
          ))}
        </ul>
        <div className="mt-3 flex justify-between border-t pt-3 font-extrabold">
          <span>مبلغ کل</span>
          <span className="text-primary tabular-nums">{formatToman(order.total_rial)}</span>
        </div>
        {payment && <p className="mt-2 text-sm text-muted-foreground">{payment}</p>}
        <p className="mt-1 text-xs text-muted-foreground">ثبت‌شده در ساعت {formatTime(order.created_at)}</p>
      </section>

      <div className="flex flex-col gap-2">
        {canCancel(order) && (
          <Button variant="outline" className="border-danger/40 text-danger hover:bg-danger/5" onClick={() => setConfirming(true)}>
            لغو سفارش
          </Button>
        )}
        <Button asChild variant="ghost">
          <Link to="/menu">بازگشت به منو</Link>
        </Button>
      </div>

      <Dialog
        open={confirming}
        onClose={() => setConfirming(false)}
        title="لغو سفارش؟"
        footer={
          <>
            <Button variant="destructive" className="flex-1" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
              {cancel.isPending ? 'در حال لغو…' : 'بله، لغو شود'}
            </Button>
            <Button variant="outline" className="flex-1" onClick={() => setConfirming(false)}>
              منصرف شدم
            </Button>
          </>
        }
      >
        <p>سفارش شمارهٔ {faDigits(order.order_number)} لغو می‌شود. این کار برگشت‌پذیر نیست.</p>
        {cancel.error && (
          <p role="alert" className="mt-3 rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
            {errorMessageFa(cancel.error)}
          </p>
        )}
      </Dialog>
    </div>
  )
}

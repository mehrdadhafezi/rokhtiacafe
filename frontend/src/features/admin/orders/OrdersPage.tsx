import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { formatTime } from '@/lib/date'
import { faDigits } from '@/lib/digits'
import { todayApiDate } from '@/lib/jalali'
import { formatToman } from '@/lib/money'
import { cn } from '@/lib/utils'

import { ordersApi, type Order } from '../api'
import { useAuth, useRefreshOrders, useToast } from '../hooks'
import { ordersKey } from '../keys'
import { orderStatusLabel, paymentStatusLabel, type OrderStatus } from '../labels'
import { Badge, Field, FormError, JalaliDateInput, PageHeader } from '../ui'
import { boardStatuses, nextTransition, secondsToExpiry, sortForBoard } from './rules'

type Tab = (typeof boardStatuses)[number] | 'HISTORY'

/** Re-render every second so expiry countdowns stay current. */
function useNow(intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}

function Countdown({ expiresAt, now }: { expiresAt: string; now: number }) {
  const s = secondsToExpiry(expiresAt, now)
  if (s <= 0) return <Badge tone="danger">مهلت تمام شد</Badge>
  const m = Math.floor(s / 60)
  const text = `${faDigits(m)}:${faDigits(s % 60).padStart(2, '۰')}`
  return (
    <Badge tone={s < 180 ? 'danger' : 'warning'}>
      <span className="tabular-nums" aria-label={`${faDigits(m)} دقیقه تا انقضا`}>
        ⏱ {text}
      </span>
    </Badge>
  )
}

export function OrderCard({ order, now, showAction = true }: { order: Order; now: number; showAction?: boolean }) {
  const { can } = useAuth()
  const refresh = useRefreshOrders()
  const toast = useToast()
  const next = nextTransition[order.status]
  const move = useMutation({
    mutationFn: () => ordersApi.transition(order.id, next!.op),
    onSuccess: (o) => {
      refresh(o)
      toast(`سفارش ${faDigits(o.order_number)}: ${orderStatusLabel[o.status]}`)
    },
    onError: () => refresh(order),
  })
  const itemsText = order.items.map((i) => `${i.name} × ${faDigits(i.quantity)}`).join('، ')

  return (
    <article
      className={cn(
        'rounded-xl border bg-surface p-3 shadow-[0_1px_2px_rgb(43_30_22/0.05)]',
        order.status === 'READY' ? 'border-success/40' : order.status === 'PENDING' ? 'border-warning/40' : 'border-border/70',
      )}
    >
      <Link
        to={`/orders/${order.id}`}
        className="block rounded-lg focus-visible:ring-[3px] focus-visible:ring-ring/50"
        aria-label={`جزئیات سفارش ${faDigits(order.order_number)}`}
      >
        <div className="flex items-start justify-between gap-2">
          <div>
            <p className="text-2xl font-black tabular-nums">#{faDigits(order.order_number)}</p>
            {order.customer_label && <p className="text-sm font-medium">{order.customer_label}</p>}
          </div>
          <div className="flex flex-col items-end gap-1 text-sm">
            <span className="text-muted-foreground tabular-nums">{formatTime(order.created_at)}</span>
            {order.status === 'PENDING' && <Countdown expiresAt={order.expires_at} now={now} />}
          </div>
        </div>
        <p className="mt-2 text-sm leading-relaxed">{itemsText}</p>
        {order.customer_note && <p className="mt-1 rounded-md bg-warning/10 px-2 py-1 text-sm text-warning">📝 {order.customer_note}</p>}
        <div className="mt-2 flex items-center justify-between gap-2">
          <span className="font-bold tabular-nums">{formatToman(order.total_rial)}</span>
          <Badge tone={order.payment_status === 'PAID' ? 'success' : order.payment_status === 'REFUNDED' ? 'warning' : 'neutral'}>
            {paymentStatusLabel[order.payment_status]}
          </Badge>
        </div>
      </Link>
      {showAction && next && can('orders.transition') && (
        <>
          <Button className="mt-3 w-full" variant={order.status === 'READY' ? 'outline' : 'default'} disabled={move.isPending} onClick={() => move.mutate()}>
            {move.isPending ? 'در حال ثبت…' : next.label}
          </Button>
          <FormError error={move.error} />
        </>
      )}
    </article>
  )
}

function Board({ tab, onTab }: { tab: Tab; onTab: (t: Tab) => void }) {
  const now = useNow()
  const q = useQuery({
    queryKey: [...ordersKey, 'board'],
    queryFn: ({ signal }) => ordersApi.list({ status: [...boardStatuses] }, signal),
    refetchInterval: 5_000,
  })
  if (q.isPending) return <LoadingState />
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />
  const by = (s: OrderStatus) => sortForBoard(q.data.items.filter((o) => o.status === s))

  return (
    <>
      {/* Phones: one column at a time. */}
      <div role="tablist" aria-label="وضعیت سفارش" className="mb-3 grid grid-cols-3 gap-2 lg:hidden">
        {boardStatuses.map((s) => (
          <button
            key={s}
            role="tab"
            aria-selected={tab === s}
            onClick={() => onTab(s)}
            className={cn(
              'flex h-12 flex-col items-center justify-center rounded-lg border text-sm font-medium leading-tight focus-visible:ring-[3px] focus-visible:ring-ring/50',
              tab === s ? 'border-primary bg-primary text-primary-foreground' : 'bg-surface',
            )}
          >
            <span>{orderStatusLabel[s]}</span>
            <span className="tabular-nums">{faDigits(by(s).length)}</span>
          </button>
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        {boardStatuses.map((s) => (
          <section key={s} aria-label={orderStatusLabel[s]} className={cn('space-y-3', tab !== s && 'hidden lg:block')}>
            <h2 className="hidden items-center justify-between font-bold lg:flex">
              {orderStatusLabel[s]} <Badge>{faDigits(by(s).length)}</Badge>
            </h2>
            {by(s).length === 0 ? (
              <EmptyState title="سفارشی نیست" className="py-8" />
            ) : (
              by(s).map((o) => <OrderCard key={o.id} order={o} now={now} />)
            )}
          </section>
        ))}
      </div>
    </>
  )
}

function History() {
  const now = useNow(60_000)
  const [date, setDate] = useState(todayApiDate())
  const q = useQuery({
    queryKey: [...ordersKey, 'history', date],
    queryFn: ({ signal }) => ordersApi.list({ date }, signal),
  })
  return (
    <div className="space-y-4">
      <Field label="تاریخ" className="max-w-sm">
        {(p) => <JalaliDateInput {...p} value={date} onChange={setDate} max={todayApiDate()} />}
      </Field>
      {q.isPending ? (
        <LoadingState />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      ) : q.data.items.length === 0 ? (
        <EmptyState title="در این روز سفارشی ثبت نشده است" />
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {q.data.items.map((o) => (
            <div key={o.id} className="space-y-1">
              <Badge tone={o.status === 'CANCELLED' ? 'danger' : o.status === 'COMPLETED' ? 'success' : 'primary'}>
                {orderStatusLabel[o.status]}
              </Badge>
              <OrderCard order={o} now={now} showAction={false} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export function OrdersPage() {
  const [params, setParams] = useSearchParams()
  const raw = params.get('tab')
  const tab: Tab = raw === 'HISTORY' || (boardStatuses as readonly string[]).includes(raw ?? '') ? (raw as Tab) : 'PENDING'
  const setTab = (t: Tab) => setParams({ tab: t }, { replace: true })
  const history = tab === 'HISTORY'

  return (
    <div>
      <PageHeader
        title="سفارش‌ها"
        description={history ? 'سفارش‌های یک روز، با همهٔ وضعیت‌ها' : 'هر ۵ ثانیه به‌روز می‌شود'}
        actions={
          <Button variant="outline" onClick={() => setTab(history ? 'PENDING' : 'HISTORY')}>
            {history ? 'سفارش‌های باز' : 'تاریخچه'}
          </Button>
        }
      />
      {history ? <History /> : <Board tab={tab} onTab={setTab} />}
    </div>
  )
}

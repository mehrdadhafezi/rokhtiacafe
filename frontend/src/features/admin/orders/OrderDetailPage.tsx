import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useParams } from 'react-router'

import { ChevronIcon } from '@/components/icons'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { isApiError } from '@/lib/api'
import { formatDateTime } from '@/lib/date'
import { faDigits } from '@/lib/digits'
import { formatToman } from '@/lib/money'

import { ordersApi, type Payment, type RefundLine } from '../api'
import { useAuth, useRefreshOrders, useToast } from '../hooks'
import { orderKey } from '../keys'
import { cancelReasonLabel, orderStatusLabel, paymentKindLabel, paymentMethodLabel, paymentStatusLabel } from '../labels'
import { Badge, Card, FormError } from '../ui'
import { CancelDialog, HandBackDialog, PaymentDialog, RefundDialog, VoidPaymentDialog } from './dialogs'
import { canCancel, canRecordPayment, canRefund, canVoidPayment, nextTransition, remainingRial } from './rules'

export function OrderDetailPage() {
  const id = Number(useParams().id)
  const { can } = useAuth()
  const refresh = useRefreshOrders()
  const toast = useToast()
  const q = useQuery({
    queryKey: orderKey(id),
    queryFn: ({ signal }) => ordersApi.get(id, signal),
    enabled: Number.isSafeInteger(id) && id > 0,
    refetchInterval: (query) => (query.state.data && ['COMPLETED', 'CANCELLED'].includes(query.state.data.status) ? false : 5_000),
  })
  const [dialog, setDialog] = useState<'pay' | 'cancel' | 'refund' | null>(null)
  const [voiding, setVoiding] = useState<Payment | null>(null)
  const [handBack, setHandBack] = useState<RefundLine[] | null>(null)
  const move = useMutation({
    mutationFn: () => ordersApi.transition(id, nextTransition[q.data!.status]!.op),
    onSuccess: (o) => {
      refresh(o)
      toast(`سفارش ${faDigits(o.order_number)}: ${orderStatusLabel[o.status]}`)
    },
    onError: () => refresh({ id }),
  })

  if (q.isPending) return <LoadingState />
  if (q.isError) {
    return isApiError(q.error, 'NOT_FOUND') ? (
      <EmptyState title="سفارش پیدا نشد" action={<Link to="/orders">بازگشت به سفارش‌ها</Link>} />
    ) : (
      <ErrorState error={q.error} onRetry={() => void q.refetch()} />
    )
  }
  const o = q.data
  const next = nextTransition[o.status]
  const owed = o.status === 'COMPLETED' && o.refunded_rial === 0 && remainingRial(o) > 0
  const hasActions =
    (!!next && can('orders.transition')) ||
    (can('payments.record') && canRecordPayment(o)) ||
    (can('payments.refund') && canRefund(o)) ||
    (can('orders.transition') && canCancel(o)) ||
    owed ||
    !!move.error
  const showCost = o.items.some((i) => i.cogs_rial !== undefined || i.estimated_cost_rial !== undefined)

  const timeline: [string, string | null][] = [
    ['ثبت سفارش', o.created_at],
    ['پذیرش', o.accepted_at],
    ['آماده شد', o.ready_at],
    ['تحویل', o.completed_at],
    ['لغو', o.cancelled_at],
  ]

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Link
          to="/orders"
          aria-label="بازگشت به سفارش‌ها"
          className="grid size-11 place-items-center rounded-full hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <ChevronIcon />
        </Link>
        <h1 className="text-2xl font-extrabold">سفارش #{faDigits(o.order_number)}</h1>
        <Badge tone={o.status === 'CANCELLED' ? 'danger' : o.status === 'COMPLETED' ? 'success' : 'primary'}>{orderStatusLabel[o.status]}</Badge>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_20rem]">
        <div className="space-y-4">
          <Card>
            {o.customer_label && <p className="font-bold">به نام {o.customer_label}</p>}
            {o.customer_note && <p className="mt-2 rounded-md bg-warning/10 px-3 py-2 text-warning">📝 {o.customer_note}</p>}
            <ul className="mt-2 divide-y">
              {o.items.map((i) => (
                <li key={i.id} className="flex items-start justify-between gap-3 py-2">
                  <div>
                    <p className="font-medium">
                      {i.name} <span className="text-muted-foreground tabular-nums">× {faDigits(i.quantity)}</span>
                    </p>
                    {i.wasted_quantity > 0 && <p className="text-sm text-danger">ضایعات: {faDigits(i.wasted_quantity)}</p>}
                    {/* COGS exists only for tracked items; untracked ones may carry a labelled estimate (F8). */}
                    {showCost && i.inventory_mode === 'TRACKED' && i.cogs_rial != null && (
                      <p className="text-xs text-muted-foreground">بهای تمام‌شده {formatToman(i.cogs_rial)}</p>
                    )}
                    {showCost && i.inventory_mode === 'UNTRACKED' && i.estimated_cost_rial != null && (
                      <p className="text-xs text-muted-foreground">هزینهٔ تخمینی {formatToman(i.estimated_cost_rial)}</p>
                    )}
                  </div>
                  <span className="tabular-nums">{formatToman(i.line_total_rial)}</span>
                </li>
              ))}
            </ul>
            <dl className="mt-2 space-y-1 border-t pt-3">
              <div className="flex justify-between text-lg font-extrabold">
                <dt>مبلغ کل</dt>
                <dd className="text-primary tabular-nums">{formatToman(o.total_rial)}</dd>
              </div>
              <div className="flex justify-between">
                <dt>دریافت‌شده</dt>
                <dd className="tabular-nums">{formatToman(o.paid_rial)}</dd>
              </div>
              {o.refunded_rial > 0 && (
                <div className="flex justify-between text-warning">
                  <dt>بازپرداخت ثبت‌شده</dt>
                  <dd className="tabular-nums">{formatToman(o.refunded_rial)}</dd>
                </div>
              )}
              {o.status !== 'CANCELLED' && o.refunded_rial === 0 && remainingRial(o) > 0 && (
                <div className="flex justify-between font-bold">
                  <dt>مانده</dt>
                  <dd className="tabular-nums">{formatToman(remainingRial(o))}</dd>
                </div>
              )}
              <div className="flex justify-between">
                <dt>وضعیت پرداخت</dt>
                <dd>{paymentStatusLabel[o.payment_status]}</dd>
              </div>
            </dl>
          </Card>

          <Card aria-labelledby="payments-title">
            <h2 id="payments-title" className="font-bold">
              پرداخت‌ها
            </h2>
            {o.payments.length === 0 ? (
              <p className="mt-2 text-sm text-muted-foreground">هنوز پرداختی ثبت نشده است.</p>
            ) : (
              <ul className="mt-2 divide-y">
                {o.payments.map((p) => (
                  <li key={p.id} className="flex flex-wrap items-center justify-between gap-2 py-2">
                    <div className={p.voided_at ? 'text-muted-foreground line-through' : undefined}>
                      <span className="font-medium">
                        {paymentKindLabel[p.kind]} {paymentMethodLabel[p.method]}
                      </span>{' '}
                      <span className="tabular-nums">{formatToman(p.amount_rial)}</span>
                      <p className="text-xs text-muted-foreground">{formatDateTime(p.created_at)}</p>
                      {p.note && <p className="text-sm">{p.note}</p>}
                    </div>
                    {p.voided_at ? (
                      <Badge tone="danger">باطل شده{p.void_reason ? `: ${p.void_reason}` : ''}</Badge>
                    ) : (
                      can('payments.void') &&
                      canVoidPayment(o, p) && (
                        <Button variant="outline" size="sm" className="h-11" onClick={() => setVoiding(p)}>
                          ابطال
                        </Button>
                      )
                    )}
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>

        <div className="space-y-4">
          {hasActions && (
            <Card aria-label="اقدام‌ها" className="space-y-2">
              {next && can('orders.transition') && (
                <Button className="w-full" size="lg" disabled={move.isPending} onClick={() => move.mutate()}>
                  {move.isPending ? 'در حال ثبت…' : next.label}
                </Button>
              )}
              <FormError error={move.error} />
              {can('payments.record') && canRecordPayment(o) && (
                <Button className="w-full" variant="outline" onClick={() => setDialog('pay')}>
                  ثبت پرداخت
                </Button>
              )}
              {can('payments.refund') && canRefund(o) && (
                <Button className="w-full" variant="outline" onClick={() => setDialog('refund')}>
                  بازپرداخت کامل
                </Button>
              )}
              {can('orders.transition') && canCancel(o) && (
                <Button className="w-full border-danger/40 text-danger hover:bg-danger/5" variant="outline" onClick={() => setDialog('cancel')}>
                  لغو سفارش
                </Button>
              )}
              {owed && (
                <p className="rounded-lg bg-warning/10 px-3 py-2 text-sm text-warning">
                  این سفارش تحویل شده ولی {formatToman(remainingRial(o))} از آن دریافت نشده است.
                </p>
              )}
            </Card>
          )}

          <Card aria-labelledby="timeline-title">
            <h2 id="timeline-title" className="font-bold">
              زمان‌ها
            </h2>
            <dl className="mt-2 space-y-1 text-sm">
              {timeline
                .filter(([, t]) => t)
                .map(([label, t]) => (
                  <div key={label} className="flex justify-between gap-2">
                    <dt className="text-muted-foreground">{label}</dt>
                    <dd>{formatDateTime(t!)}</dd>
                  </div>
                ))}
            </dl>
            {o.cancel_reason && (
              <p className="mt-2 rounded-lg bg-muted px-3 py-2 text-sm">
                {cancelReasonLabel[o.cancel_reason]}
                {o.cancel_note && ` — ${o.cancel_note}`}
              </p>
            )}
          </Card>
        </div>
      </div>

      {dialog === 'pay' && <PaymentDialog order={o} open onClose={() => setDialog(null)} />}
      {dialog === 'cancel' && <CancelDialog order={o} open onClose={() => setDialog(null)} onRefunds={setHandBack} />}
      {dialog === 'refund' && <RefundDialog order={o} open onClose={() => setDialog(null)} onRefunds={setHandBack} />}
      <VoidPaymentDialog order={o} payment={voiding} onClose={() => setVoiding(null)} />
      <HandBackDialog refunds={handBack} onClose={() => setHandBack(null)} />
    </div>
  )
}

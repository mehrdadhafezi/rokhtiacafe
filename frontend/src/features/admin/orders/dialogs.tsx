import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'

import { Dialog } from '@/components/Dialog'
import { Button } from '@/components/ui/button'
import { faDigits } from '@/lib/digits'
import { formatToman, parseTomanToRial } from '@/lib/money'
import { cn } from '@/lib/utils'

import { ordersApi, type Order, type Payment, type RefundLine } from '../api'
import { rialToInput } from '../format'
import { useIdempotencyKey, useRefreshOrders, useToast } from '../hooks'
import { paymentMethodLabel, type PaymentMethod } from '../labels'
import { ConfirmDialog, Field, FormError, MoneyInput, NumberInput, TextArea } from '../ui'
import { remainingRial, wasteableItems } from './rules'


/** Money staff must hand back after a cancellation or refund: recording it does not move money. */
export function HandBackDialog({ refunds, onClose }: { refunds: RefundLine[] | null; onClose: () => void }) {
  const byMethod = new Map<PaymentMethod, number>()
  for (const r of refunds ?? []) byMethod.set(r.method, (byMethod.get(r.method) ?? 0) + r.amount_rial)
  return (
    <Dialog
      open={!!refunds && refunds.length > 0}
      onClose={onClose}
      title="بازپرداخت به مشتری"
      footer={
        <Button className="flex-1" onClick={onClose}>
          پرداخت کردم
        </Button>
      }
    >
      <p className="text-muted-foreground">بازپرداخت در سیستم ثبت شد. این مبلغ را خودتان به مشتری برگردانید:</p>
      <ul className="mt-3 space-y-2">
        {[...byMethod].map(([method, amount]) => (
          <li key={method} className="flex items-center justify-between rounded-lg bg-warning/10 px-3 py-3 font-bold text-warning">
            <span>{method === 'CASH' ? 'نقدی از صندوق' : 'برگشت تراکنش کارتخوان'}</span>
            <span className="tabular-nums">{formatToman(amount)}</span>
          </li>
        ))}
      </ul>
    </Dialog>
  )
}

export function PaymentDialog({ order, open, onClose }: { order: Order; open: boolean; onClose: () => void }) {
  const remaining = remainingRial(order)
  const [method, setMethod] = useState<PaymentMethod>('CASH')
  const [amount, setAmount] = useState(rialToInput(remaining))
  const [note, setNote] = useState('')
  const [key, rotateKey] = useIdempotencyKey()
  const refresh = useRefreshOrders()
  const toast = useToast()
  const rial = parseTomanToRial(amount)
  const invalid = rial === null || rial <= 0 || rial > remaining

  const pay = useMutation({
    mutationFn: () => ordersApi.pay(order.id, { method, amount_rial: rial!, ...(note.trim() ? { note: note.trim() } : {}) }, key),
    onSuccess: (r) => {
      rotateKey()
      refresh(order)
      toast(`${formatToman(r.payment.amount_rial)} ${paymentMethodLabel[r.payment.method]} ثبت شد.`)
      onClose()
    },
  })

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={`ثبت پرداخت — سفارش ${faDigits(order.order_number)}`}
      footer={
        <>
          <Button className="flex-1" disabled={invalid || pay.isPending} onClick={() => pay.mutate()}>
            {pay.isPending ? 'در حال ثبت…' : 'ثبت پرداخت'}
          </Button>
          <Button variant="outline" className="flex-1" onClick={onClose}>
            انصراف
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="flex justify-between rounded-lg bg-muted px-3 py-2">
          <span>مانده</span>
          <b className="tabular-nums">{formatToman(remaining)}</b>
        </p>
        <fieldset>
          <legend className="mb-1 text-sm font-medium">روش پرداخت</legend>
          <div className="grid grid-cols-2 gap-2">
            {(['CASH', 'CARD_TERMINAL'] as const).map((m) => (
              <label
                key={m}
                className={cn(
                  'flex h-12 cursor-pointer items-center justify-center rounded-lg border font-medium has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50',
                  method === m ? 'border-primary bg-primary text-primary-foreground' : 'bg-surface hover:bg-muted',
                )}
              >
                <input type="radio" name="method" value={m} checked={method === m} onChange={() => setMethod(m)} className="sr-only" />
                {paymentMethodLabel[m]}
              </label>
            ))}
          </div>
        </fieldset>
        <Field label="مبلغ" error={rial !== null && rial > remaining ? 'بیشتر از مانده است.' : undefined}>
          {(p) => <MoneyInput {...p} value={amount} onChange={setAmount} />}
        </Field>
        <Field label="یادداشت (اختیاری)">
          {(p) => <TextArea {...p} value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} />}
        </Field>
        <FormError error={pay.error} />
      </div>
    </Dialog>
  )
}

export function CancelDialog({
  order,
  open,
  onClose,
  onRefunds,
}: {
  order: Order
  open: boolean
  onClose: () => void
  onRefunds: (r: RefundLine[]) => void
}) {
  const items = wasteableItems(order)
  const [note, setNote] = useState('')
  const [waste, setWaste] = useState<Record<number, string>>({})
  const refresh = useRefreshOrders()
  const toast = useToast()
  const wasteBody = items
    .map((i) => ({ product_id: i.product_id, quantity: Number(waste[i.product_id] || 0) }))
    .filter((w) => w.quantity > 0)
  const overWaste = items.some((i) => Number(waste[i.product_id] || 0) > i.quantity)

  const cancel = useMutation({
    mutationFn: () => ordersApi.cancel(order.id, { ...(note.trim() ? { note: note.trim() } : {}), ...(wasteBody.length ? { waste: wasteBody } : {}) }),
    onSuccess: (r) => {
      refresh(order)
      toast(`سفارش ${faDigits(order.order_number)} لغو شد.`)
      onClose()
      if (r.refunds?.length) onRefunds(r.refunds)
    },
  })

  return (
    <ConfirmDialog
      open={open}
      onClose={onClose}
      title={`لغو سفارش ${faDigits(order.order_number)}`}
      description={
        order.paid_rial > 0
          ? `این سفارش ${formatToman(order.paid_rial)} پیش‌پرداخت دارد که با لغو، بازپرداخت آن ثبت می‌شود.`
          : 'این کار برگشت‌پذیر نیست و رزرو موجودی آزاد می‌شود.'
      }
      confirmLabel="لغو سفارش"
      destructive
      pending={cancel.isPending || overWaste}
      error={cancel.error}
      onConfirm={() => cancel.mutate()}
    >
      <Field label="دلیل یا یادداشت (اختیاری)">
        {(p) => <TextArea {...p} value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} />}
      </Field>
      {items.length > 0 && (
        <fieldset className="space-y-2 rounded-lg border p-3">
          <legend className="px-1 text-sm font-medium">ضایعات (اقلامی که آماده شده و دور ریخته می‌شود)</legend>
          {items.map((i) => (
            <Field
              key={i.product_id}
              label={`${i.name} — حداکثر ${faDigits(i.quantity)}`}
              error={Number(waste[i.product_id] || 0) > i.quantity ? 'بیشتر از تعداد سفارش است.' : undefined}
            >
              {(p) => (
                <NumberInput
                  {...p}
                  placeholder="۰"
                  value={waste[i.product_id] ?? ''}
                  onChange={(v) => setWaste((w) => ({ ...w, [i.product_id]: v }))}
                />
              )}
            </Field>
          ))}
          <p className="text-xs text-muted-foreground">اقلامی که ضایع نشده‌اند به موجودی برمی‌گردند.</p>
        </fieldset>
      )}
    </ConfirmDialog>
  )
}

export function RefundDialog({
  order,
  open,
  onClose,
  onRefunds,
}: {
  order: Order
  open: boolean
  onClose: () => void
  onRefunds: (r: RefundLine[]) => void
}) {
  const refresh = useRefreshOrders()
  const refund = useMutation({
    mutationFn: (note: string) => ordersApi.refund(order.id, note),
    onSuccess: (r) => {
      refresh(order)
      onClose()
      onRefunds(r.refunds.map((p) => ({ method: p.method, amount_rial: p.amount_rial })))
    },
  })
  return (
    <ConfirmDialog
      open={open}
      onClose={onClose}
      title={`بازپرداخت کامل سفارش ${faDigits(order.order_number)}`}
      description={`کل مبلغ ${formatToman(order.paid_rial)} به همان روش‌های پرداخت، به‌عنوان بازپرداخت ثبت می‌شود. سپس باید مبلغ را خودتان به مشتری برگردانید.`}
      confirmLabel="ثبت بازپرداخت"
      destructive
      reason={{ label: 'دلیل بازپرداخت', max: 200 }}
      pending={refund.isPending}
      error={refund.error}
      onConfirm={(note) => refund.mutate(note)}
    />
  )
}

export function VoidPaymentDialog({ order, payment, onClose }: { order: Order; payment: Payment | null; onClose: () => void }) {
  const refresh = useRefreshOrders()
  const toast = useToast()
  const voidIt = useMutation({
    mutationFn: (reason: string) => ordersApi.voidPayment(payment!.id, reason),
    onSuccess: () => {
      refresh(order)
      toast('پرداخت باطل شد.')
      onClose()
    },
  })
  return (
    <ConfirmDialog
      open={payment !== null}
      onClose={onClose}
      title="ابطال پرداخت"
      description={
        payment && (
          <>
            {formatToman(payment.amount_rial)} {paymentMethodLabel[payment.method]} باطل می‌شود. فقط برای اصلاح ثبت اشتباه استفاده کنید؛ برای
            برگرداندن پول از بازپرداخت استفاده کنید.
          </>
        )
      }
      confirmLabel="ابطال پرداخت"
      destructive
      reason={{ label: 'دلیل ابطال', required: true, max: 200 }}
      pending={voidIt.isPending}
      error={voidIt.error}
      onConfirm={(reason) => voidIt.mutate(reason)}
    />
  )
}

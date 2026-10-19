import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'

import { Dialog } from '@/components/Dialog'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { formatDateTime } from '@/lib/date'
import { faDigits } from '@/lib/digits'
import { formatToman, parseTomanToRial } from '@/lib/money'
import { cn } from '@/lib/utils'

import { inventoryApi, type StockRow } from '../api'
import { rialToInput } from '../format'
import { useAuth, useIdempotencyKey, useToast } from '../hooks'
import { dashboardKey, inventoryKey } from '../keys'
import { movementTypeLabel, reasonLabel, reasonsIn, reasonsOut, type ReasonCode } from '../labels'
import { Badge, Card, Field, FormError, MoneyInput, NumberInput, PageHeader, Select, TextArea } from '../ui'

function AdjustDialog({ row, onClose }: { row: StockRow; onClose: () => void }) {
  const [direction, setDirection] = useState<'IN' | 'OUT'>('IN')
  const [quantity, setQuantity] = useState('')
  const [cost, setCost] = useState(rialToInput(row.avg_unit_cost_rial || row.last_unit_cost_rial))
  const [reason, setReason] = useState<ReasonCode>('COUNT_CORRECTION')
  const [note, setNote] = useState('')
  const [key, rotate] = useIdempotencyKey()
  const queryClient = useQueryClient()
  const toast = useToast()
  const qty = Number(quantity || 0)
  const unit = parseTomanToRial(cost)
  const reasons = direction === 'IN' ? reasonsIn : reasonsOut
  const tooMany = direction === 'OUT' && qty > row.available
  const invalid = qty < 1 || !note.trim() || tooMany || (direction === 'IN' && unit === null)

  const save = useMutation({
    mutationFn: () =>
      inventoryApi.adjust(
        row.product_id,
        { direction, quantity: qty, reason_code: reason, note: note.trim(), ...(direction === 'IN' ? { unit_cost_rial: unit! } : {}) },
        key,
      ),
    onSuccess: (m) => {
      rotate()
      void queryClient.invalidateQueries({ queryKey: inventoryKey })
      void queryClient.invalidateQueries({ queryKey: dashboardKey })
      toast(`موجودی ${row.name}: ${faDigits(m.on_hand_after)}`)
      onClose()
    },
  })

  return (
    <Dialog
      open
      onClose={onClose}
      title={`اصلاح موجودی ${row.name}`}
      footer={
        <>
          <Button className="flex-1" disabled={invalid || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? 'در حال ثبت…' : 'ثبت اصلاح'}
          </Button>
          <Button variant="outline" className="flex-1" onClick={onClose}>
            انصراف
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="rounded-lg bg-muted px-3 py-2 text-sm">
          موجودی فعلی {faDigits(row.on_hand)} — رزروشده {faDigits(row.reserved)} — قابل فروش {faDigits(row.available)}
        </p>
        <fieldset>
          <legend className="mb-1 text-sm font-medium">نوع اصلاح</legend>
          <div className="grid grid-cols-2 gap-2">
            {(['IN', 'OUT'] as const).map((d) => (
              <label
                key={d}
                className={cn(
                  'flex h-12 cursor-pointer items-center justify-center rounded-lg border font-medium has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50',
                  direction === d ? 'border-primary bg-primary text-primary-foreground' : 'bg-surface hover:bg-muted',
                )}
              >
                <input
                  type="radio"
                  name="direction"
                  className="sr-only"
                  checked={direction === d}
                  onChange={() => {
                    setDirection(d)
                    setReason(d === 'IN' ? 'COUNT_CORRECTION' : 'DAMAGED')
                  }}
                />
                {d === 'IN' ? 'افزایش' : 'کاهش'}
              </label>
            ))}
          </div>
        </fieldset>
        <Field label="علت">
          {(p) => (
            <Select {...p} value={reason} onChange={(e) => setReason(e.target.value as ReasonCode)}>
              {reasons.map((r) => (
                <option key={r} value={r}>
                  {reasonLabel[r]}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="تعداد" error={tooMany ? 'بیشتر از موجودی قابل فروش است.' : undefined}>
          {(p) => <NumberInput {...p} value={quantity} onChange={setQuantity} />}
        </Field>
        {direction === 'IN' ? (
          <Field label="بهای هر واحد" hint="برای محاسبهٔ میانگین بهای تمام‌شده.">
            {(p) => <MoneyInput {...p} value={cost} onChange={setCost} />}
          </Field>
        ) : (
          <p className="text-sm text-muted-foreground">کاهش با میانگین بهای فعلی ثبت می‌شود.</p>
        )}
        <Field label="توضیح (الزامی)">{(p) => <TextArea {...p} maxLength={300} value={note} onChange={(e) => setNote(e.target.value)} />}</Field>
        <FormError error={save.error} />
      </div>
    </Dialog>
  )
}

function MovementsDialog({ row, onClose }: { row: StockRow; onClose: () => void }) {
  const q = useInfiniteQuery({
    queryKey: [...inventoryKey, 'movements', row.product_id],
    queryFn: ({ pageParam, signal }) => inventoryApi.movements(row.product_id, pageParam, signal),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => last.next_before_id ?? undefined,
  })
  const items = q.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <Dialog open onClose={onClose} title={`گردش موجودی ${row.name}`} className="sm:max-w-2xl">
      {q.isPending ? (
        <LoadingState />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="گردشی ثبت نشده است" />
      ) : (
        <>
          <ul className="divide-y">
            {items.map((m) => (
              <li key={m.id} className="flex flex-wrap items-start justify-between gap-2 py-2">
                <div>
                  <p className="font-medium">
                    {movementTypeLabel[m.movement_type]}
                    {m.reason_code && <span className="text-muted-foreground"> — {reasonLabel[m.reason_code]}</span>}
                    {m.order_id && (
                      <Link to={`/orders/${m.order_id}`} className="ms-2 text-sm text-primary underline-offset-4 hover:underline">
                        سفارش
                      </Link>
                    )}
                  </p>
                  <p className="text-xs text-muted-foreground">{formatDateTime(m.created_at)}</p>
                  {m.note && <p className="text-sm">{m.note}</p>}
                </div>
                <div className="text-end text-sm tabular-nums">
                  <p className={cn('font-bold', m.quantity_delta > 0 ? 'text-success' : 'text-danger')} dir="ltr">
                    {m.quantity_delta > 0 ? '+' : '−'}
                    {faDigits(Math.abs(m.quantity_delta))}
                  </p>
                  <p className="text-muted-foreground">مانده {faDigits(m.on_hand_after)}</p>
                  <p className="text-muted-foreground">ارزش {formatToman(m.value_after_rial)}</p>
                </div>
              </li>
            ))}
          </ul>
          {q.hasNextPage && (
            <Button variant="outline" className="mt-3 w-full" disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
              موارد قدیمی‌تر
            </Button>
          )}
        </>
      )}
    </Dialog>
  )
}

export function InventoryPage() {
  const { can } = useAuth()
  const showCost = can('inventory.view_cost')
  const manage = can('inventory.manage')
  const q = useQuery({ queryKey: inventoryKey, queryFn: ({ signal }) => inventoryApi.list(signal), refetchInterval: 30_000 })
  const [adjusting, setAdjusting] = useState<StockRow | null>(null)
  const [viewing, setViewing] = useState<StockRow | null>(null)

  if (q.isPending) return <LoadingState />
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />
  const rows = q.data.items
  const totalValue = rows.reduce((s, r) => s + (r.inventory_value_rial ?? 0), 0)

  return (
    <div className="space-y-4">
      <PageHeader
        title="موجودی"
        description="فقط اقلام «با کنترل موجودی». رزروشده یعنی در سفارش‌های باز."
        actions={
          manage && (
            <Button asChild variant="outline">
              <Link to="/purchases">ثبت خرید</Link>
            </Button>
          )
        }
      />
      {showCost && (
        <Card>
          <p className="text-sm text-muted-foreground">ارزش کل موجودی (میانگین بهای تمام‌شده)</p>
          <p className="mt-1 text-2xl font-black text-primary tabular-nums">{formatToman(totalValue)}</p>
        </Card>
      )}
      {rows.length === 0 ? (
        <EmptyState title="کالای شمارشی تعریف نشده است" description="در «منو و محصولات» نوع موجودی را «با کنترل موجودی» بگذارید." />
      ) : (
        <ul className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {rows.map((r) => (
            <li key={r.product_id}>
              <Card className={cn('h-full', r.low && 'border-warning/50', !r.is_active && 'opacity-60')}>
                <div className="flex items-start justify-between gap-2">
                  <p className="font-bold">{r.name}</p>
                  {r.available === 0 ? <Badge tone="danger">تمام شد</Badge> : r.low ? <Badge tone="warning">کم است</Badge> : null}
                </div>
                <dl className="mt-3 grid grid-cols-3 gap-2 text-center">
                  {(
                    [
                      ['موجود', r.on_hand],
                      ['رزرو', r.reserved],
                      ['قابل فروش', r.available],
                    ] as const
                  ).map(([label, n]) => (
                    <div key={label} className="rounded-lg bg-muted px-1 py-2">
                      <dt className="text-xs text-muted-foreground">{label}</dt>
                      <dd className="text-xl font-black tabular-nums">{faDigits(n)}</dd>
                    </div>
                  ))}
                </dl>
                {r.threshold !== null && <p className="mt-2 text-sm text-muted-foreground">حد هشدار: {faDigits(r.threshold)}</p>}
                {showCost && (
                  <p className="mt-1 text-sm text-muted-foreground">
                    میانگین بها {formatToman(r.avg_unit_cost_rial ?? 0)} · ارزش {formatToman(r.inventory_value_rial ?? 0)}
                  </p>
                )}
                {(manage || showCost) && (
                  <div className="mt-3 flex gap-2">
                    {manage && (
                      <Button variant="outline" className="flex-1" onClick={() => setAdjusting(r)}>
                        اصلاح موجودی
                      </Button>
                    )}
                    {showCost && (
                      <Button variant="ghost" className="flex-1" onClick={() => setViewing(r)}>
                        گردش
                      </Button>
                    )}
                  </div>
                )}
              </Card>
            </li>
          ))}
        </ul>
      )}
      {adjusting && <AdjustDialog row={adjusting} onClose={() => setAdjusting(null)} />}
      {viewing && <MovementsDialog row={viewing} onClose={() => setViewing(null)} />}
    </div>
  )
}

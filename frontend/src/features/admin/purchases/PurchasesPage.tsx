import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'

import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { faDigits } from '@/lib/digits'
import { todayApiDate } from '@/lib/jalali'
import { formatToman, parseTomanToRial } from '@/lib/money'

import { catalogApi, inventoryApi, type Purchase } from '../api'
import { formatApiDate, monthStartApiDate } from '../format'
import { useIdempotencyKey, useToast } from '../hooks'
import { dashboardKey, inventoryKey, productsKey, purchasesKey, reportsKey } from '../keys'
import { Badge, Card, ConfirmDialog, Field, FormError, JalaliDateInput, MoneyInput, NumberInput, PageHeader, Select, TextArea, inputClass } from '../ui'

function PurchaseForm({ products }: { products: { id: number; name: string }[] }) {
  const [productId, setProductId] = useState(String(products[0]?.id ?? ''))
  const [quantity, setQuantity] = useState('')
  const [cost, setCost] = useState('')
  const [date, setDate] = useState(todayApiDate())
  const [supplier, setSupplier] = useState('')
  const [note, setNote] = useState('')
  const [key, rotate] = useIdempotencyKey()
  const queryClient = useQueryClient()
  const toast = useToast()
  const unit = parseTomanToRial(cost)
  const qty = Number(quantity || 0)
  const invalid = !productId || qty < 1 || unit === null

  const save = useMutation({
    mutationFn: () =>
      inventoryApi.createPurchase(
        {
          product_id: Number(productId),
          quantity: qty,
          unit_cost_rial: unit!,
          purchased_on: date,
          ...(supplier.trim() ? { supplier: supplier.trim() } : {}),
          ...(note.trim() ? { note: note.trim() } : {}),
        },
        key,
      ),
    onSuccess: (p) => {
      rotate()
      for (const k of [purchasesKey, inventoryKey, dashboardKey, reportsKey]) void queryClient.invalidateQueries({ queryKey: k })
      toast(`خرید ${faDigits(p.quantity)} عدد به مبلغ ${formatToman(p.total_cost_rial)} ثبت شد.`)
      setQuantity('')
      setCost('')
      setNote('')
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!invalid) save.mutate()
  }

  return (
    <Card>
      <h2 className="mb-3 font-bold">ثبت خرید</h2>
      <form onSubmit={submit} className="grid gap-4 sm:grid-cols-2" noValidate>
        <Field label="کالا">
          {(p) => (
            <Select {...p} value={productId} onChange={(e) => setProductId(e.target.value)}>
              {products.map((pr) => (
                <option key={pr.id} value={pr.id}>
                  {pr.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="تاریخ خرید">{(p) => <JalaliDateInput {...p} value={date} onChange={setDate} max={todayApiDate()} />}</Field>
        <Field label="تعداد">{(p) => <NumberInput {...p} value={quantity} onChange={setQuantity} />}</Field>
        <Field label="قیمت هر واحد">{(p) => <MoneyInput {...p} value={cost} onChange={setCost} />}</Field>
        <Field label="تأمین‌کننده (اختیاری)">
          {(p) => <input {...p} className={inputClass} maxLength={80} value={supplier} onChange={(e) => setSupplier(e.target.value)} />}
        </Field>
        <Field label="یادداشت (اختیاری)">{(p) => <TextArea {...p} maxLength={300} value={note} onChange={(e) => setNote(e.target.value)} />}</Field>
        <div className="space-y-2 sm:col-span-2">
          {unit !== null && qty > 0 && (
            <p className="rounded-lg bg-muted px-3 py-2">
              جمع: <b className="tabular-nums">{formatToman(unit * qty)}</b>
            </p>
          )}
          <FormError error={save.error} />
          <Button type="submit" disabled={invalid || save.isPending} className="w-full sm:w-auto">
            {save.isPending ? 'در حال ثبت…' : 'ثبت خرید'}
          </Button>
        </div>
      </form>
    </Card>
  )
}

export function PurchasesPage() {
  const products = useQuery({ queryKey: productsKey, queryFn: ({ signal }) => catalogApi.products(signal) })
  const [from, setFrom] = useState(monthStartApiDate)
  const [to, setTo] = useState(todayApiDate())
  const [productFilter, setProductFilter] = useState('')
  const list = useQuery({
    queryKey: [...purchasesKey, from, to, productFilter],
    queryFn: ({ signal }) => inventoryApi.purchases({ from, to, product_id: productFilter ? Number(productFilter) : undefined }, signal),
  })
  const [voiding, setVoiding] = useState<Purchase | null>(null)
  const queryClient = useQueryClient()
  const toast = useToast()
  const voidIt = useMutation({
    mutationFn: (reason: string) => inventoryApi.voidPurchase(voiding!.id, reason),
    onSuccess: () => {
      for (const k of [purchasesKey, inventoryKey, dashboardKey, reportsKey]) void queryClient.invalidateQueries({ queryKey: k })
      toast('خرید باطل شد.')
      setVoiding(null)
    },
  })

  if (products.isPending) return <LoadingState />
  if (products.isError) return <ErrorState error={products.error} onRetry={() => void products.refetch()} />
  const tracked = products.data.items.filter((p) => p.inventory_mode === 'TRACKED')
  const name = (id: number) => products.data.items.find((p) => p.id === id)?.name ?? `#${faDigits(id)}`
  const live = list.data?.items.filter((p) => !p.voided_at) ?? []
  const total = live.reduce((s, p) => s + p.total_cost_rial, 0)

  return (
    <div className="space-y-4">
      <PageHeader title="خریدها" description="خرید کالاهای شمارشی؛ موجودی و میانگین بها را به‌روز می‌کند. خرید هزینه نیست و هنگام فروش به بهای تمام‌شده تبدیل می‌شود." />
      {tracked.length === 0 ? (
        <EmptyState title="کالای شمارشی تعریف نشده است" description="ابتدا در «منو و محصولات» محصولی «با کنترل موجودی» بسازید." />
      ) : (
        <PurchaseForm products={tracked} />
      )}

      <Card>
        <h2 className="mb-3 font-bold">سابقهٔ خرید</h2>
        <div className="mb-4 grid gap-3 md:grid-cols-3">
          <Field label="از تاریخ">{(p) => <JalaliDateInput {...p} value={from} onChange={setFrom} max={to} />}</Field>
          <Field label="تا تاریخ">{(p) => <JalaliDateInput {...p} value={to} onChange={setTo} max={todayApiDate()} />}</Field>
          <Field label="کالا">
            {(p) => (
              <Select {...p} value={productFilter} onChange={(e) => setProductFilter(e.target.value)}>
                <option value="">همه</option>
                {tracked.map((pr) => (
                  <option key={pr.id} value={pr.id}>
                    {pr.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        {list.isPending ? (
          <LoadingState />
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : list.data.items.length === 0 ? (
          <EmptyState title="خریدی در این بازه ثبت نشده است" />
        ) : (
          <>
            <p className="mb-2 text-sm">
              جمع خریدهای معتبر: <b className="tabular-nums">{formatToman(total)}</b>
            </p>
            <ul className="divide-y">
              {list.data.items.map((p) => (
                <li key={p.id} className="flex flex-wrap items-center justify-between gap-2 py-3">
                  <div className={p.voided_at ? 'text-muted-foreground' : undefined}>
                    <p className="font-medium">
                      {name(p.product_id)} — {faDigits(p.quantity)} عدد × {formatToman(p.unit_cost_rial)}
                    </p>
                    <p className="text-sm text-muted-foreground">
                      {formatApiDate(p.purchased_on)}
                      {p.supplier && ` · ${p.supplier}`}
                    </p>
                    {p.note && <p className="text-sm">{p.note}</p>}
                    {p.voided_at && <p className="text-sm text-danger">باطل شده: {p.void_reason}</p>}
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="font-bold tabular-nums">{formatToman(p.total_cost_rial)}</span>
                    {p.voided_at ? (
                      <Badge tone="danger">باطل</Badge>
                    ) : (
                      <Button variant="outline" onClick={() => setVoiding(p)}>
                        ابطال
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          </>
        )}
      </Card>

      <ConfirmDialog
        open={voiding !== null}
        onClose={() => setVoiding(null)}
        title="ابطال خرید"
        description="ابطال فقط وقتی ممکن است که از این خرید چیزی فروخته یا مصرف نشده باشد. موجودی و ارزش آن دقیقاً برگردانده می‌شود."
        confirmLabel="ابطال خرید"
        destructive
        reason={{ label: 'دلیل ابطال', required: true, max: 200 }}
        pending={voidIt.isPending}
        error={voidIt.error}
        onConfirm={(r) => voidIt.mutate(r)}
      />
    </div>
  )
}

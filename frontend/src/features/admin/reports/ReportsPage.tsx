import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'

import { ChevronIcon } from '@/components/icons'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { faDigits } from '@/lib/digits'
import { apiDateToJalali, jalaliMonthNames, jalaliMonthRange, shiftJalaliMonth, todayApiDate } from '@/lib/jalali'
import { formatToman } from '@/lib/money'
import { cn } from '@/lib/utils'

import { financeApi, type ByMethod, type Figures } from '../api'
import { formatApiDate } from '../format'
import { reportsKey } from '../keys'
import { expenseCategoryLabel, inventoryModeLabel, paymentMethodLabel, type ExpenseCategory } from '../labels'
import { Badge, Card, Field, JalaliDateInput, PageHeader } from '../ui'

function Row({ label, value, strong, tone, hint }: { label: string; value: number; strong?: boolean; tone?: 'danger' | 'success'; hint?: string }) {
  return (
    <div className={cn('flex items-baseline justify-between gap-3 py-1.5', strong && 'border-t pt-2 text-lg font-extrabold')}>
      <dt>
        {label}
        {hint && <span className="block text-xs font-normal text-muted-foreground">{hint}</span>}
      </dt>
      <dd className={cn('tabular-nums whitespace-nowrap', tone === 'danger' && 'text-danger', tone === 'success' && 'text-success')} dir="ltr">
        {value < 0 ? '−' : ''}
        {formatToman(Math.abs(value))}
      </dd>
    </div>
  )
}

function Section({ title, children, className }: { title: string; children: ReactNode; className?: string }) {
  return (
    <Card className={className}>
      <h2 className="mb-2 font-bold">{title}</h2>
      {children}
    </Card>
  )
}

function MethodsTable({ t }: { t: Figures }) {
  const methods = Object.keys(paymentMethodLabel) as (keyof ByMethod)[]
  const sum = (m: ByMethod) => methods.reduce((s, k) => s + (m[k] ?? 0), 0)
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[30rem] text-sm [&_td]:whitespace-nowrap">
        <thead>
          <tr className="text-muted-foreground">
            <th className="py-1 text-start font-medium">روش</th>
            <th className="py-1 text-end font-medium">دریافت‌شده</th>
            <th className="py-1 text-end font-medium">بازپرداخت ثبت‌شده</th>
            <th className="py-1 text-end font-medium">خالص دریافتی</th>
          </tr>
        </thead>
        <tbody className="tabular-nums">
          {methods.map((m) => (
            <tr key={m} className="border-t">
              <th scope="row" className="py-2 text-start font-medium">
                {paymentMethodLabel[m]}
              </th>
              <td className="py-2 text-end">{formatToman(t.payments_received[m] ?? 0)}</td>
              <td className="py-2 text-end">{formatToman(t.refunds_recorded[m] ?? 0)}</td>
              <td className="py-2 text-end font-bold">{formatToman(t.collected[m] ?? 0)}</td>
            </tr>
          ))}
          <tr className="border-t font-extrabold">
            <th scope="row" className="py-2 text-start">
              جمع
            </th>
            <td className="py-2 text-end">{formatToman(sum(t.payments_received))}</td>
            <td className="py-2 text-end">{formatToman(sum(t.refunds_recorded))}</td>
            <td className="py-2 text-end">{formatToman(sum(t.collected))}</td>
          </tr>
        </tbody>
      </table>
    </div>
  )
}

export function ReportsPage() {
  const today = apiDateToJalali(todayApiDate())
  const [month, setMonth] = useState({ year: today.year, month: today.month })
  const [custom, setCustom] = useState<{ from: string; to: string } | null>(null)
  const range = custom ?? jalaliMonthRange(month.year, month.month)
  const isCurrent = month.year === today.year && month.month === today.month

  const summary = useQuery({
    queryKey: [...reportsKey, 'summary', range.from, range.to],
    queryFn: ({ signal }) => financeApi.summary(range.from, range.to, signal),
  })
  const products = useQuery({
    queryKey: [...reportsKey, 'products', range.from, range.to],
    queryFn: ({ signal }) => financeApi.products(range.from, range.to, signal),
  })
  const receivables = useQuery({ queryKey: [...reportsKey, 'receivables'], queryFn: ({ signal }) => financeApi.receivables(signal) })

  const step = (delta: number) => {
    setCustom(null)
    setMonth((m) => shiftJalaliMonth(m.year, m.month, delta))
  }
  const title = custom ? `${formatApiDate(range.from)} تا ${formatApiDate(range.to)}` : `${jalaliMonthNames[month.month - 1]} ${faDigits(month.year)}`

  return (
    <div className="space-y-4">
      <PageHeader title="گزارش‌ها" description="ارقام بر پایهٔ روز کاری تهران؛ فروش هنگام تحویل سفارش ثبت می‌شود." />

      <Card className="space-y-3">
        <div className="flex items-center justify-between gap-2">
          <Button variant="ghost" size="icon" aria-label="ماه قبل" onClick={() => step(-1)}>
            <ChevronIcon />
          </Button>
          <p className="text-center text-lg font-extrabold">{title}</p>
          <Button variant="ghost" size="icon" aria-label="ماه بعد" disabled={!custom && isCurrent} onClick={() => step(1)}>
            <ChevronIcon className="rotate-180" />
          </Button>
        </div>
        <details>
          <summary className="flex h-11 cursor-pointer items-center text-sm font-medium text-primary">بازهٔ دلخواه</summary>
          <div className="grid gap-3 pt-2 md:grid-cols-2">
            <Field label="از تاریخ">
              {(p) => <JalaliDateInput {...p} value={range.from} max={range.to} onChange={(from) => setCustom({ from, to: range.to })} />}
            </Field>
            <Field label="تا تاریخ">
              {(p) => <JalaliDateInput {...p} value={range.to} max={todayApiDate()} onChange={(to) => setCustom({ from: range.from, to })} />}
            </Field>
          </div>
        </details>
      </Card>

      {summary.isPending ? (
        <LoadingState />
      ) : summary.isError ? (
        <ErrorState error={summary.error} onRetry={() => void summary.refetch()} />
      ) : (
        (() => {
          const t = summary.data.totals
          const activeDays = summary.data.daily.filter((d) => d.orders > 0 || d.expenses_rial > 0 || d.purchases_rial > 0 || d.revenue_rial !== 0)
          const expenses = Object.entries(summary.data.expenses_by_category).filter(([, v]) => v)
          return (
            <>
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                {(
                  [
                    ['درآمد', t.revenue_rial],
                    ['سود ناخالص', t.gross_profit_rial],
                    ['نتیجه عملیاتی تخمینی', t.operating_result_rial],
                    ['تعداد سفارش', null],
                  ] as const
                ).map(([label, v]) => (
                  <Card key={label}>
                    <p className="text-sm text-muted-foreground">{label}</p>
                    <p className={cn('mt-1 text-2xl font-black tabular-nums', v !== null && v < 0 && 'text-danger')}>
                      {v === null ? faDigits(t.orders) : (v < 0 ? '−' : '') + formatToman(Math.abs(v))}
                    </p>
                  </Card>
                ))}
              </div>

              <div className="grid gap-4 lg:grid-cols-2">
                <Section title="فروش و سود">
                  <dl>
                    <Row label="فروش ناخالص" value={t.gross_sales_rial} />
                    <Row label="بازپرداخت فروش" value={-t.sales_refunds_rial} />
                    <Row label="درآمد" value={t.revenue_rial} strong />
                    <Row label="بهای تمام‌شدهٔ کالای فروش‌رفته" value={-t.cogs_rial} />
                    <Row label="سود ناخالص" value={t.gross_profit_rial} strong />
                    <Row label="کسری و ضایعات موجودی" value={-t.inventory_losses_rial} />
                    <Row label="اضافهٔ موجودی" value={t.inventory_gains_rial} />
                    <Row label="هزینه‌ها" value={-t.expenses_rial} />
                    <Row
                      label="نتیجه عملیاتی تخمینی"
                      value={t.operating_result_rial}
                      strong
                      tone={t.operating_result_rial < 0 ? 'danger' : 'success'}
                      hint="بدون استهلاک، مالیات و هزینهٔ اقلام بدون کنترل موجودی"
                    />
                  </dl>
                  <p className="mt-2 text-sm text-muted-foreground">میانگین هر سفارش: {formatToman(t.average_order_value_rial)}</p>
                </Section>

                <Section title="دریافت و بازپرداخت">
                  <MethodsTable t={t} />
                  <p className="mt-2 rounded-lg bg-warning/10 px-3 py-2 text-sm text-warning">
                    «بازپرداخت ثبت‌شده» یعنی مبلغی که باید به مشتری برگردد؛ ثبت آن پولی جابه‌جا نمی‌کند و کارکنان باید آن را پرداخت کنند.
                  </p>
                </Section>

                <Section title="سایر ارقام (خارج از نتیجهٔ عملیاتی)">
                  <dl>
                    <Row label="خرید کالا" value={t.purchases_rial} hint="خرید هزینه نیست؛ هنگام فروش به بهای تمام‌شده تبدیل می‌شود." />
                    <Row label="بازپرداخت پیش‌پرداخت سفارش‌های لغوشده" value={t.prepayment_refunds_rial} />
                    <Row label="ضایعات تخمینی (اقلام بدون کنترل موجودی)" value={t.estimated_waste_rial} hint="تخمینی؛ در هیچ جمعی حساب نمی‌شود." />
                  </dl>
                </Section>

                <Section title="هزینه‌ها به تفکیک دسته">
                  {expenses.length === 0 ? (
                    <p className="text-sm text-muted-foreground">هزینه‌ای در این بازه ثبت نشده است.</p>
                  ) : (
                    <dl>
                      {expenses.map(([c, v]) => (
                        <Row key={c} label={expenseCategoryLabel[c as ExpenseCategory] ?? c} value={v!} />
                      ))}
                    </dl>
                  )}
                </Section>
              </div>

              <Section title="روزبه‌روز">
                {activeDays.length === 0 ? (
                  <p className="text-sm text-muted-foreground">در این بازه فعالیتی ثبت نشده است.</p>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full min-w-[36rem] text-sm">
                      <thead>
                        <tr className="text-muted-foreground">
                          <th className="py-1 text-start font-medium">روز</th>
                          <th className="py-1 text-end font-medium">سفارش</th>
                          <th className="py-1 text-end font-medium">درآمد</th>
                          <th className="py-1 text-end font-medium">سود ناخالص</th>
                          <th className="py-1 text-end font-medium">هزینه</th>
                          <th className="py-1 text-end font-medium">نتیجه تخمینی</th>
                        </tr>
                      </thead>
                      <tbody className="tabular-nums">
                        {activeDays.map((d) => (
                          <tr key={d.date} className="border-t">
                            <th scope="row" className="py-2 text-start font-medium whitespace-nowrap">
                              {formatApiDate(d.date)}
                            </th>
                            <td className="py-2 text-end">{faDigits(d.orders)}</td>
                            <td className="py-2 text-end">{formatToman(d.revenue_rial)}</td>
                            <td className="py-2 text-end">{formatToman(d.gross_profit_rial)}</td>
                            <td className="py-2 text-end">{formatToman(d.expenses_rial)}</td>
                            <td className={cn('py-2 text-end', d.operating_result_rial < 0 && 'text-danger')} dir="ltr">
                              {d.operating_result_rial < 0 ? '−' : ''}
                              {formatToman(Math.abs(d.operating_result_rial))}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </Section>
            </>
          )
        })()
      )}

      <Section title="فروش محصولات">
        {products.isPending ? (
          <LoadingState />
        ) : products.isError ? (
          <ErrorState error={products.error} onRetry={() => void products.refetch()} />
        ) : products.data.items.length === 0 ? (
          <EmptyState title="در این بازه فروشی نبوده است" className="py-6" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[40rem] text-sm">
              <thead>
                <tr className="text-muted-foreground">
                  <th className="py-1 text-start font-medium">محصول</th>
                  <th className="py-1 text-end font-medium">تعداد</th>
                  <th className="py-1 text-end font-medium">فروش</th>
                  <th className="py-1 text-end font-medium">سود ناخالص</th>
                  <th className="py-1 text-end font-medium">حاشیهٔ تخمینی</th>
                </tr>
              </thead>
              <tbody className="tabular-nums">
                {products.data.items.map((p) => (
                  <tr key={p.product_id} className="border-t align-top">
                    <th scope="row" className="py-2 text-start font-medium">
                      {p.name}
                      <span className="block text-xs font-normal text-muted-foreground">{inventoryModeLabel[p.inventory_mode]}</span>
                    </th>
                    <td className="py-2 text-end">{faDigits(p.quantity)}</td>
                    <td className="py-2 text-end">{formatToman(p.revenue_rial)}</td>
                    <td className="py-2 text-end">{p.inventory_mode === 'TRACKED' ? formatToman(p.gross_profit_rial) : '—'}</td>
                    <td className="py-2 text-end">
                      {p.estimated_margin_rial !== null ? (
                        <>
                          {formatToman(p.estimated_margin_rial)} <Badge tone="warning">تخمینی</Badge>
                        </>
                      ) : (
                        '—'
                      )}
                      {p.lines_without_estimate > 0 && (
                        <span className="block text-xs text-muted-foreground">{faDigits(p.lines_without_estimate)} ردیف بدون تخمین</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <p className="mt-2 text-xs text-muted-foreground">
          فروش هر محصول پیش از بازپرداخت سفارش است. حاشیهٔ تخمینی فقط بر پایهٔ هزینهٔ تخمینی واردشده است و به سود ناخالص اضافه نمی‌شود.
        </p>
      </Section>

      <Section title="طلب از مشتریان (هم‌اکنون)">
        {receivables.isPending ? (
          <LoadingState />
        ) : receivables.isError ? (
          <ErrorState error={receivables.error} onRetry={() => void receivables.refetch()} />
        ) : receivables.data.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">همهٔ سفارش‌های تحویل‌شده تسویه شده‌اند.</p>
        ) : (
          <>
            <p className="mb-2 font-bold">جمع: {formatToman(receivables.data.total_outstanding_rial)}</p>
            <ul className="divide-y">
              {receivables.data.items.map((r) => (
                <li key={r.order_id} className="flex items-center justify-between gap-2 py-2">
                  <Link to={`/orders/${r.order_id}`} className="inline-flex h-11 items-center font-medium text-primary underline-offset-4 hover:underline">
                    سفارش #{faDigits(r.order_number)} — {formatApiDate(r.business_date)}
                  </Link>
                  <span className="tabular-nums">{formatToman(r.outstanding_rial)}</span>
                </li>
              ))}
            </ul>
          </>
        )}
      </Section>

      <footer className="rounded-xl bg-muted px-4 py-3 text-sm text-muted-foreground">
        <p className="font-bold text-foreground">محدودیت‌های گزارش</p>
        <ul className="mt-1 list-disc space-y-1 ps-5">
          <li>«نتیجه عملیاتی تخمینی» سود خالص نیست: استهلاک، مالیات و بهای اقلام بدون کنترل موجودی در آن نیامده است.</li>
          <li>بهای تمام‌شده فقط برای اقلام «با کنترل موجودی» و با میانگین موزون محاسبه می‌شود.</li>
          <li>ارقام «تخمینی» از هزینه‌های تخمینی واردشده در منو می‌آیند و در جمع‌ها حساب نمی‌شوند.</li>
          <li>طلب از مشتریان وضعیت همین لحظه است و به بازهٔ انتخابی بستگی ندارد.</li>
        </ul>
      </footer>
    </div>
  )
}

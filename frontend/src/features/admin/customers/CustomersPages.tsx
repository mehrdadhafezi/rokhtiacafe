import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useDeferredValue, useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'

import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { isApiError } from '@/lib/api'
import { formatDate, formatDateTime } from '@/lib/date'
import { faDigits, toPersianDigits } from '@/lib/digits'
import { apiDateToJalali, jalaliMonthRange, todayApiDate } from '@/lib/jalali'
import { formatToman } from '@/lib/money'
import { cn } from '@/lib/utils'

import { customersApi, type CustomerFigures, type CustomerOrder, type ExportFormat } from '../api'
import { formatApiDate, formError } from '../format'
import { customersKey } from '../keys'
import { cancelReasonLabel, orderStatusLabel, paymentStatusLabel } from '../labels'
import { Badge, Card, Field, JalaliDateInput, PageHeader, inputClass } from '../ui'

/** Café customers (accounts of the /menu app). Not staff: staff users stay under /users. */

function Money({ rial, tone }: { rial: number; tone?: 'danger' | 'success' }) {
  return (
    <span className={cn('tabular-nums whitespace-nowrap', tone === 'danger' && 'text-danger', tone === 'success' && 'text-success')}>
      {rial < 0 ? '−' : ''}
      {formatToman(Math.abs(rial))}
    </span>
  )
}

function Stat({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      <p className="text-sm text-muted-foreground">{label}</p>
      <p className="mt-1 text-lg font-extrabold">{children}</p>
      {hint && <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

function PhoneText({ phone }: { phone: string }) {
  return (
    <span dir="ltr" className="tabular-nums">
      {toPersianDigits(phone)}
    </span>
  )
}

/** Download buttons for an export; plain links so the browser saves the server's file. */
function ExportLinks({ href, label }: { href: (f: ExportFormat) => string; label: string }) {
  return (
    <div className="flex flex-wrap gap-2" aria-label={label}>
      <Button asChild variant="outline" size="sm">
        <a href={href('xlsx')} download>
          دانلود اکسل (XLSX)
        </a>
      </Button>
      <Button asChild variant="outline" size="sm">
        <a href={href('csv')} download>
          دانلود CSV
        </a>
      </Button>
    </div>
  )
}

function useRange(initial: () => { from: string; to: string }) {
  const [range, setRange] = useState(initial)
  const valid = range.from <= range.to
  const controls = (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="از تاریخ">
        {(p) => <JalaliDateInput {...p} value={range.from} max={range.to} onChange={(from) => setRange((r) => ({ ...r, from }))} />}
      </Field>
      <Field label="تا تاریخ">
        {(p) => <JalaliDateInput {...p} value={range.to} max={todayApiDate()} onChange={(to) => setRange((r) => ({ ...r, to }))} />}
      </Field>
    </div>
  )
  return { range, valid, controls }
}

function currentMonth() {
  const t = apiDateToJalali(todayApiDate())
  const m = jalaliMonthRange(t.year, t.month)
  return { from: m.from, to: todayApiDate() < m.to ? todayApiDate() : m.to }
}

/** Last 90 days ending today (business dates). */
function last90Days() {
  const to = todayApiDate()
  const d = new Date(`${to}T12:00:00Z`)
  d.setUTCDate(d.getUTCDate() - 89)
  return { from: d.toISOString().slice(0, 10), to }
}

// ---- /customers ----

export function CustomersPage() {
  const [search, setSearch] = useState('')
  const deferred = useDeferredValue(search)
  const list = useInfiniteQuery({
    queryKey: [...customersKey, 'list', deferred.trim()],
    queryFn: ({ pageParam, signal }) => customersApi.list(deferred, pageParam, signal),
    initialPageParam: null as number | null,
    getNextPageParam: (last) => last.next_before_id ?? undefined,
  })
  const rows = list.data?.pages.flatMap((p) => p.items) ?? []

  return (
    <div className="space-y-4">
      <PageHeader
        title="مشتریان"
        description="مشتریانی که در منوی آنلاین حساب ساخته‌اند (جدا از کاربران کارکنان). شمارهٔ موبایل‌ها تأیید پیامکی ندارند."
        actions={
          <Button asChild variant="outline">
            <Link to="/customers/report">گزارش خرید مشتریان</Link>
          </Button>
        }
      />
      <Card>
        <Field label="جست‌وجو" hint="بخشی از نام یا شمارهٔ موبایل.">
          {(p) => (
            <input
              {...p}
              type="search"
              className={inputClass}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="مثلاً ۰۹۱۲ یا مریم"
              autoComplete="off"
            />
          )}
        </Field>
      </Card>

      {list.isPending ? (
        <LoadingState />
      ) : list.isError ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : rows.length === 0 ? (
        <EmptyState title={deferred.trim() ? 'مشتری‌ای با این مشخصات پیدا نشد' : 'هنوز مشتری‌ای ثبت‌نام نکرده است'} />
      ) : (
        <Card className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[46rem] text-sm [&_td]:px-3 [&_td]:py-2.5 [&_th]:px-3 [&_th]:py-2">
              <thead className="bg-muted/50 text-muted-foreground">
                <tr>
                  <th className="text-start font-medium">مشتری</th>
                  <th className="text-start font-medium">موبایل</th>
                  <th className="text-start font-medium">عضویت</th>
                  <th className="text-end font-medium">سفارش‌ها</th>
                  <th className="text-end font-medium">تحویل‌شده</th>
                  <th className="text-end font-medium">فروش تکمیل‌شده</th>
                  <th className="text-start font-medium">آخرین سفارش</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((c) => (
                  <tr key={c.id} className="border-t">
                    <td>
                      <Link to={`/customers/${c.id}`} className="font-medium text-primary underline-offset-4 hover:underline">
                        {c.first_name} {c.last_name}
                      </Link>
                      {!c.is_active && <Badge tone="danger" className="ms-2">غیرفعال</Badge>}
                    </td>
                    <td>
                      <PhoneText phone={c.phone} />
                    </td>
                    <td className="whitespace-nowrap">{formatDate(c.created_at)}</td>
                    <td className="text-end tabular-nums">{faDigits(c.orders)}</td>
                    <td className="text-end tabular-nums">{faDigits(c.completed_orders)}</td>
                    <td className="text-end">
                      <Money rial={c.completed_gross_rial} />
                      {c.completed_refunded_rial > 0 && (
                        <span className="block text-xs text-danger">
                          بازپرداخت: <Money rial={c.completed_refunded_rial} />
                        </span>
                      )}
                    </td>
                    <td className="whitespace-nowrap text-muted-foreground">{c.last_order_at ? formatDateTime(c.last_order_at) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      {list.hasNextPage && (
        <Button variant="outline" className="w-full" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
          {list.isFetchingNextPage ? 'در حال بارگذاری…' : 'مشتریان بیشتر'}
        </Button>
      )}
    </div>
  )
}

// ---- /customers/:id ----

function OrderRow({ o }: { o: CustomerOrder }) {
  const status = o.status === 'PENDING' && o.expired ? 'منقضی شده' : orderStatusLabel[o.status]
  const tone = o.status === 'COMPLETED' ? 'success' : o.status === 'CANCELLED' || o.expired ? 'danger' : 'primary'
  return (
    <li className="space-y-1 py-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Link to={`/orders/${o.id}`} className="font-bold text-primary underline-offset-4 hover:underline">
          سفارش {faDigits(o.order_number)} · {formatApiDate(o.business_date)}
        </Link>
        <span className="flex flex-wrap gap-1.5">
          <Badge tone={tone}>{status}</Badge>
          {o.payment_status && <Badge tone={o.payment_status === 'PAID' ? 'success' : o.payment_status === 'REFUNDED' ? 'warning' : 'neutral'}>{paymentStatusLabel[o.payment_status]}</Badge>}
        </span>
      </div>
      <p className="text-sm text-muted-foreground">{o.items.map((i) => `${i.name} × ${faDigits(i.quantity)}`).join('، ')}</p>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
        <span>
          جمع: <Money rial={o.total_rial} />
        </span>
        <span>
          پرداخت‌شده: <Money rial={o.paid_rial} />
        </span>
        {o.refunded_rial > 0 && (
          <span>
            بازپرداخت: <Money rial={o.refunded_rial} tone="danger" />
          </span>
        )}
        {o.cancel_reason && <span className="text-muted-foreground">{cancelReasonLabel[o.cancel_reason]}</span>}
      </div>
    </li>
  )
}

export function CustomerDetailPage() {
  const id = Number(useParams().id)
  const valid = Number.isSafeInteger(id) && id > 0
  const { range, valid: rangeOk, controls } = useRange(last90Days)
  const detail = useQuery({ queryKey: [...customersKey, 'detail', id], queryFn: ({ signal }) => customersApi.get(id, signal), enabled: valid })
  const orders = useInfiniteQuery({
    queryKey: [...customersKey, 'orders', id, range.from, range.to],
    queryFn: ({ pageParam, signal }) => customersApi.orders(id, range.from, range.to, pageParam, signal),
    initialPageParam: null as number | null,
    getNextPageParam: (last) => last.next_before_id ?? undefined,
    enabled: valid && rangeOk,
  })

  if (!valid || isApiError(detail.error, 'NOT_FOUND')) return <EmptyState title="مشتری پیدا نشد" />
  if (detail.isPending) return <LoadingState />
  if (detail.isError) return <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
  const { customer: c, totals: t } = detail.data
  const rows = orders.data?.pages.flatMap((p) => p.items) ?? []

  return (
    <div className="space-y-4">
      <PageHeader
        title={`${c.first_name} ${c.last_name}`}
        description="سفارش‌هایی که هنگام ثبت به حساب این مشتری متصل شده‌اند؛ سفارش‌های مهمان هرگز بر پایهٔ نام یا شماره به حساب نسبت داده نمی‌شوند."
        actions={
          <Button asChild variant="ghost">
            <Link to="/customers">همهٔ مشتریان</Link>
          </Button>
        }
      />
      <Card className="space-y-1 text-sm">
        <p>
          موبایل: <PhoneText phone={c.phone} /> <Badge className="ms-1">{c.phone_verified ? 'تأییدشده' : 'تأییدنشده'}</Badge>
        </p>
        <p>عضویت: {formatDateTime(c.created_at)}</p>
        <p>آخرین ورود: {c.last_login_at ? formatDateTime(c.last_login_at) : '—'}</p>
        {!c.is_active && <Badge tone="danger">حساب غیرفعال است</Badge>}
      </Card>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="کل سفارش‌ها">{faDigits(t.orders)}</Stat>
        <Stat label="تحویل‌شده / لغوشده / باز">
          {faDigits(t.completed_orders)} / {faDigits(t.cancelled_orders)} / {faDigits(t.open_orders)}
        </Stat>
        <Stat label="فروش تکمیل‌شده (ناخالص)" hint="جمع سفارش‌های تحویل‌شده پیش از بازپرداخت">
          <Money rial={t.completed_gross_rial} />
        </Stat>
        <Stat label="درآمد پس از بازپرداخت">
          <Money rial={t.completed_revenue_rial} />
        </Stat>
        <Stat label="بازپرداخت سفارش‌های تحویل‌شده">
          <Money rial={t.completed_refunded_rial} tone={t.completed_refunded_rial ? 'danger' : undefined} />
        </Stat>
        <Stat label="پرداخت دریافتی (ناخالص)" hint="پرداخت‌های باطل‌نشده، پیش از کسر بازپرداخت">
          <Money rial={t.payments_received_rial} />
        </Stat>
        <Stat label="بازپرداخت ثبت‌شده" hint="بازپرداخت سفارش‌های تحویل‌شده و پیش‌پرداخت سفارش‌های لغوشده">
          <Money rial={t.refunds_recorded_rial} tone={t.refunds_recorded_rial ? 'danger' : undefined} />
        </Stat>
        <Stat label="خالص دریافتی" hint="پرداخت دریافتی منهای بازپرداخت ثبت‌شده">
          <Money rial={t.net_collected_rial} />
        </Stat>
        <Stat label="مطالبات (اکنون)" hint="تحویل‌شده، بدون بازپرداخت و کامل پرداخت‌نشده">
          <Money rial={t.receivable_rial} tone={t.receivable_rial ? 'danger' : undefined} />
        </Stat>
      </div>

      <Card className="space-y-3">
        <h2 className="font-bold">سفارش‌ها در بازه</h2>
        {controls}
        {!rangeOk && <p className="text-sm text-danger">تاریخ شروع باید پیش از تاریخ پایان باشد.</p>}
        {rangeOk && (
          <ExportLinks label="دانلود اقلام سفارش‌های این مشتری" href={(f) => customersApi.ordersExportUrl(id, f, range.from, range.to)} />
        )}
        {orders.isPending ? (
          <LoadingState />
        ) : orders.isError ? (
          <p role="alert" className="text-sm text-danger">
            {formError(orders.error)}
          </p>
        ) : rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">در این بازه سفارشی ثبت نشده است.</p>
        ) : (
          <ul className="divide-y">
            {rows.map((o) => (
              <OrderRow key={o.id} o={o} />
            ))}
          </ul>
        )}
        {orders.hasNextPage && (
          <Button variant="outline" className="w-full" onClick={() => void orders.fetchNextPage()} disabled={orders.isFetchingNextPage}>
            سفارش‌های قدیمی‌تر
          </Button>
        )}
      </Card>
    </div>
  )
}

// ---- /customers/report ----

const columns: { key: keyof CustomerFigures; label: string; money?: boolean; tone?: 'danger' }[] = [
  { key: 'orders_placed', label: 'سفارش ثبت‌شده' },
  { key: 'completed_orders', label: 'تحویل‌شده' },
  { key: 'cancelled_orders', label: 'لغوشده' },
  { key: 'open_orders', label: 'باز (اکنون)' },
  { key: 'gross_sales_rial', label: 'فروش ناخالص', money: true },
  { key: 'sales_refunds_rial', label: 'بازپرداخت فروش', money: true, tone: 'danger' },
  { key: 'revenue_rial', label: 'درآمد', money: true },
  { key: 'payments_received_rial', label: 'دریافتی', money: true },
  { key: 'refunds_recorded_rial', label: 'بازپرداخت ثبت‌شده', money: true, tone: 'danger' },
  { key: 'net_collected_rial', label: 'خالص دریافتی', money: true },
  { key: 'receivable_rial', label: 'مطالبات (اکنون)', money: true, tone: 'danger' },
]

const definitionsFa: [string, string][] = [
  ['فروش ناخالص', 'جمع مبلغ سفارش‌هایی که در بازه تحویل شده‌اند (فروش هنگام تحویل شناسایی می‌شود).'],
  ['بازپرداخت فروش', 'بازپرداخت‌های ثبت‌شده در بازه برای سفارش‌های تحویل‌شده.'],
  ['درآمد', 'فروش ناخالص منهای بازپرداخت فروش.'],
  ['دریافتی', 'پرداخت‌های باطل‌نشدهٔ ثبت‌شده در بازه (نقدی و کارتخوان).'],
  ['بازپرداخت ثبت‌شده', 'همهٔ بازپرداخت‌های ثبت‌شده در بازه، از جمله پیش‌پرداخت سفارش‌های لغوشده (که هرگز درآمد نیستند).'],
  ['خالص دریافتی', 'دریافتی منهای بازپرداخت ثبت‌شده.'],
  ['مطالبات', 'سفارش‌های تحویل‌شدهٔ بدون بازپرداخت که هنوز کامل پرداخت نشده‌اند، در لحظهٔ گزارش؛ به بازه محدود نیست.'],
  ['باز (اکنون)', 'سفارش‌های در انتظار، در حال آماده‌سازی یا آماده، در لحظهٔ گزارش؛ به بازه محدود نیست و سفارش‌های ثبت‌شده پیش از بازه را هم می‌شمارد.'],
  ['فهرست مشتریان', 'هر مشتری که در بازه فعالیت داشته یا اکنون مطالبات یا سفارش باز دارد.'],
  ['انتساب', 'فقط سفارش‌هایی که هنگام ثبت به حساب مشتری متصل شده‌اند؛ سفارش‌های مهمان در این گزارش نیستند.'],
]

export function CustomerReportPage() {
  const { range, valid, controls } = useRange(currentMonth)
  const report = useQuery({
    queryKey: [...customersKey, 'report', range.from, range.to],
    queryFn: ({ signal }) => customersApi.report(range.from, range.to, signal),
    enabled: valid,
  })
  return (
    <div className="space-y-4">
      <PageHeader
        title="گزارش خرید مشتریان"
        description="ارقام بر پایهٔ روز کاری تهران و همان تعریف‌های گزارش مالی کافه."
        actions={
          <Button asChild variant="ghost">
            <Link to="/customers">همهٔ مشتریان</Link>
          </Button>
        }
      />
      <Card className="space-y-3">
        {controls}
        {valid ? (
          <ExportLinks label="دانلود گزارش" href={(f) => customersApi.reportExportUrl(f, range.from, range.to)} />
        ) : (
          <p className="text-sm text-danger">تاریخ شروع باید پیش از تاریخ پایان باشد.</p>
        )}
        <details className="text-sm">
          <summary className="flex h-11 cursor-pointer items-center font-medium text-primary">تعریف شاخص‌ها</summary>
          <dl className="space-y-1 pb-2">
            {definitionsFa.map(([k, v]) => (
              <div key={k}>
                <dt className="inline font-medium">{k}: </dt>
                <dd className="inline text-muted-foreground">{v}</dd>
              </div>
            ))}
          </dl>
        </details>
      </Card>

      {!valid ? null : report.isPending ? (
        <LoadingState />
      ) : report.isError ? (
        <ErrorState error={report.error} onRetry={() => void report.refetch()} />
      ) : report.data.customers.length === 0 ? (
        <EmptyState title="در این بازه خریدی از مشتریان دارای حساب ثبت نشده است" />
      ) : (
        <Card className="p-0">
          <p className="px-4 pt-3 text-sm text-muted-foreground">
            {formatApiDate(report.data.from)} تا {formatApiDate(report.data.to)} · {faDigits(report.data.customers.length)} مشتری
          </p>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[64rem] text-sm [&_td]:px-3 [&_td]:py-2.5 [&_th]:px-3 [&_th]:py-2">
              <thead className="text-muted-foreground">
                <tr>
                  <th className="text-start font-medium">مشتری</th>
                  {columns.map((col) => (
                    <th key={col.key} className="text-end font-medium">
                      {col.label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {report.data.customers.map((r) => (
                  <tr key={r.customer_id} className="border-t">
                    <td>
                      <Link to={`/customers/${r.customer_id}`} className="font-medium text-primary underline-offset-4 hover:underline">
                        {r.first_name} {r.last_name}
                      </Link>
                      <span className="block text-xs text-muted-foreground">
                        <PhoneText phone={r.phone} />
                      </span>
                    </td>
                    {columns.map((col) => (
                      <td key={col.key} className="text-end">
                        {col.money ? <Money rial={r[col.key]} tone={r[col.key] && col.tone ? col.tone : undefined} /> : <span className="tabular-nums">{faDigits(r[col.key])}</span>}
                      </td>
                    ))}
                  </tr>
                ))}
                <tr className="border-t-2 font-extrabold">
                  <td>جمع</td>
                  {columns.map((col) => (
                    <td key={col.key} className="text-end">
                      {col.money ? <Money rial={report.data.totals[col.key]} /> : <span className="tabular-nums">{faDigits(report.data.totals[col.key])}</span>}
                    </td>
                  ))}
                </tr>
              </tbody>
            </table>
          </div>
          <p className="px-4 pb-3 text-xs text-muted-foreground">
            بازپرداخت پیش‌پرداخت سفارش‌های لغوشده: <Money rial={report.data.totals.prepayment_refunds_rial} /> (جریان نقدی، نه درآمد)
          </p>
        </Card>
      )}
    </div>
  )
}

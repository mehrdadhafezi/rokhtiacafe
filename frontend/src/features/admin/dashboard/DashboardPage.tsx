import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'

import { ErrorState, LoadingState } from '@/components/states'
import { faDigits } from '@/lib/digits'
import { formatDate } from '@/lib/date'
import { formatToman } from '@/lib/money'
import { cn } from '@/lib/utils'

import { dashboardApi } from '../api'
import { faClock } from '../format'
import { useAuth, useToast } from '../hooks'
import { dashboardKey } from '../keys'
import { orderStatusLabel } from '../labels'
import { Badge, Card, ConfirmDialog, PageHeader, Switch } from '../ui'

export function DashboardPage() {
  const { can } = useAuth()
  const toast = useToast()
  const queryClient = useQueryClient()
  const q = useQuery({ queryKey: dashboardKey, queryFn: ({ signal }) => dashboardApi.get(signal), refetchInterval: 15_000 })
  const [confirmSwitch, setConfirmSwitch] = useState<boolean | null>(null)
  const toggle = useMutation({
    mutationFn: dashboardApi.setOrdering,
    onSuccess: (r) => {
      setConfirmSwitch(null)
      toast(r.switch_open ? 'سفارش‌گیری آنلاین دوباره فعال شد.' : 'سفارش‌گیری آنلاین متوقف شد.')
      void queryClient.invalidateQueries({ queryKey: dashboardKey })
    },
  })

  if (q.isPending) return <LoadingState />
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />
  const d = q.data
  const hours = d.ordering_hours

  return (
    <div className="space-y-4">
      <PageHeader title="داشبورد" description={formatDate(new Date())} />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        {(['PENDING', 'PREPARING', 'READY'] as const).map((s) => (
          <Link
            key={s}
            to={`/orders?tab=${s}`}
            className={cn(
              'rounded-xl border p-4 transition hover:shadow-sm focus-visible:ring-[3px] focus-visible:ring-ring/50',
              s === 'PENDING' && d.open_counts[s] > 0 ? 'border-warning/40 bg-warning/10' : 'border-border/70 bg-surface',
            )}
          >
            <p className="text-sm text-muted-foreground">{orderStatusLabel[s]}</p>
            <p className="mt-1 text-3xl font-black tabular-nums">{faDigits(d.open_counts[s])}</p>
          </Link>
        ))}
        <Card>
          <p className="text-sm text-muted-foreground">سفارش‌های امروز</p>
          <p className="mt-1 text-3xl font-black tabular-nums">{faDigits(d.today_orders)}</p>
        </Card>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card aria-labelledby="ordering-title">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 id="ordering-title" className="font-bold">
                سفارش‌گیری آنلاین
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                ساعت کاری {faClock(hours.opens_at)} تا {faClock(hours.closes_at)}
              </p>
            </div>
            <Badge tone={d.ordering_open ? 'success' : 'danger'}>{d.ordering_open ? 'باز' : 'بسته'}</Badge>
          </div>
          {!d.ordering_switch_open ? (
            <p className="mt-3 rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">سفارش‌گیری با کلید اضطراری متوقف شده است.</p>
          ) : (
            !d.ordering_open && <p className="mt-3 text-sm text-muted-foreground">خارج از ساعت کاری است.</p>
          )}
          {can('ordering.toggle') && (
            <div className="mt-3 flex items-center justify-between gap-3 border-t pt-3">
              <span className="text-sm font-medium">کلید اضطراری سفارش‌گیری</span>
              <Switch
                label="سفارش‌گیری آنلاین فعال باشد"
                checked={d.ordering_switch_open}
                disabled={toggle.isPending}
                onChange={(next) => setConfirmSwitch(next)}
              />
            </div>
          )}
        </Card>

        {d.today_revenue_rial !== undefined && (
          <Card>
            <h2 className="font-bold">درآمد امروز</h2>
            <p className="mt-2 text-3xl font-black text-primary tabular-nums">{formatToman(d.today_revenue_rial)}</p>
            <p className="mt-1 text-sm text-muted-foreground">فروش سفارش‌های تحویل‌شده منهای بازپرداخت‌ها</p>
            <Link to="/reports" className="mt-2 inline-flex h-11 items-center text-sm font-medium text-primary underline-offset-4 hover:underline">
              مشاهدهٔ گزارش‌ها
            </Link>
          </Card>
        )}
      </div>

      <Card aria-labelledby="low-title">
        <h2 id="low-title" className="font-bold">
          هشدار موجودی
        </h2>
        {d.low_stock.length === 0 ? (
          <p className="mt-2 text-sm text-muted-foreground">موجودی همهٔ اقلام کافی است.</p>
        ) : (
          <ul className="mt-2 divide-y">
            {d.low_stock.map((l) => (
              <li key={l.product_id} className="flex items-center justify-between gap-3 py-2">
                <span className="font-medium">{l.name}</span>
                <span className="text-sm tabular-nums">
                  <Badge tone={l.available === 0 ? 'danger' : 'warning'}>
                    {l.available === 0 ? 'تمام شد' : `${faDigits(l.available)} عدد مانده`}
                  </Badge>
                  <span className="ms-2 text-muted-foreground">حد هشدار {faDigits(l.threshold)}</span>
                </span>
              </li>
            ))}
          </ul>
        )}
        {can('inventory.view_qty') && d.low_stock.length > 0 && (
          <Link to="/inventory" className="mt-1 inline-flex h-11 items-center text-sm font-medium text-primary underline-offset-4 hover:underline">
            رفتن به موجودی
          </Link>
        )}
      </Card>

      <ConfirmDialog
        open={confirmSwitch !== null}
        onClose={() => setConfirmSwitch(null)}
        title={confirmSwitch ? 'فعال‌کردن سفارش‌گیری' : 'توقف سفارش‌گیری آنلاین'}
        description={
          confirmSwitch
            ? 'مشتریان دوباره می‌توانند در ساعت کاری از منو سفارش دهند.'
            : 'تا زمانی که دوباره فعالش کنید، مشتریان نمی‌توانند سفارش آنلاین ثبت کنند. سفارش‌های ثبت‌شده تغییری نمی‌کنند. با راه‌اندازی دوبارهٔ سرور، سفارش‌گیری به حالت عادی برمی‌گردد.'
        }
        confirmLabel={confirmSwitch ? 'فعال شود' : 'متوقف شود'}
        destructive={confirmSwitch === false}
        pending={toggle.isPending}
        error={toggle.error}
        onConfirm={() => confirmSwitch !== null && toggle.mutate(confirmSwitch)}
      />
    </div>
  )
}

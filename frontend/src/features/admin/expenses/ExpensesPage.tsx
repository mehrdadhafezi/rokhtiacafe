import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'

import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { todayApiDate } from '@/lib/jalali'
import { formatToman, parseTomanToRial } from '@/lib/money'

import { financeApi, type Expense } from '../api'
import { formatApiDate, monthStartApiDate } from '../format'
import { useIdempotencyKey, useToast } from '../hooks'
import { expensesKey, reportsKey } from '../keys'
import { expenseCategories, expenseCategoryLabel, type ExpenseCategory } from '../labels'
import { Badge, Card, ConfirmDialog, Field, FormError, JalaliDateInput, MoneyInput, PageHeader, Select, inputClass } from '../ui'

function ExpenseForm() {
  const [category, setCategory] = useState<ExpenseCategory>('INGREDIENTS')
  const [amount, setAmount] = useState('')
  const [date, setDate] = useState(todayApiDate())
  const [description, setDescription] = useState('')
  const [key, rotate] = useIdempotencyKey()
  const queryClient = useQueryClient()
  const toast = useToast()
  const rial = parseTomanToRial(amount)
  const invalid = !rial || !description.trim()
  const save = useMutation({
    mutationFn: () => financeApi.createExpense({ category, amount_rial: rial!, expense_date: date, description: description.trim() }, key),
    onSuccess: (e) => {
      rotate()
      void queryClient.invalidateQueries({ queryKey: expensesKey })
      void queryClient.invalidateQueries({ queryKey: reportsKey })
      toast(`هزینهٔ ${formatToman(e.amount_rial)} ثبت شد.`)
      setAmount('')
      setDescription('')
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!invalid) save.mutate()
  }
  return (
    <Card>
      <h2 className="mb-3 font-bold">ثبت هزینه</h2>
      <form onSubmit={submit} className="grid gap-4 sm:grid-cols-2" noValidate>
        <Field label="دسته">
          {(p) => (
            <Select {...p} value={category} onChange={(e) => setCategory(e.target.value as ExpenseCategory)}>
              {expenseCategories.map((c) => (
                <option key={c} value={c}>
                  {expenseCategoryLabel[c]}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="تاریخ">{(p) => <JalaliDateInput {...p} value={date} onChange={setDate} max={todayApiDate()} />}</Field>
        <Field label="مبلغ">{(p) => <MoneyInput {...p} value={amount} onChange={setAmount} />}</Field>
        <Field label="شرح">
          {(p) => <input {...p} className={inputClass} maxLength={200} value={description} onChange={(e) => setDescription(e.target.value)} />}
        </Field>
        <div className="space-y-2 sm:col-span-2">
          <FormError error={save.error} />
          <Button type="submit" disabled={invalid || save.isPending} className="w-full sm:w-auto">
            {save.isPending ? 'در حال ثبت…' : 'ثبت هزینه'}
          </Button>
        </div>
      </form>
    </Card>
  )
}

export function ExpensesPage() {
  const [from, setFrom] = useState(monthStartApiDate)
  const [to, setTo] = useState(todayApiDate())
  const [category, setCategory] = useState<ExpenseCategory | ''>('')
  const list = useQuery({
    queryKey: [...expensesKey, from, to, category],
    queryFn: ({ signal }) => financeApi.expenses({ from, to, category: category || undefined }, signal),
  })
  const [voiding, setVoiding] = useState<Expense | null>(null)
  const queryClient = useQueryClient()
  const toast = useToast()
  const voidIt = useMutation({
    mutationFn: (reason: string) => financeApi.voidExpense(voiding!.id, reason),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: expensesKey })
      void queryClient.invalidateQueries({ queryKey: reportsKey })
      toast('هزینه باطل شد.')
      setVoiding(null)
    },
  })
  const live = list.data?.items.filter((e) => !e.voided_at) ?? []
  const total = live.reduce((s, e) => s + e.amount_rial, 0)

  return (
    <div className="space-y-4">
      <PageHeader title="هزینه‌ها" description="هزینه‌های جاری مثل اجاره، قبوض و حقوق. خرید کالای شمارشی را در «خریدها» ثبت کنید." />
      <ExpenseForm />
      <Card>
        <h2 className="mb-3 font-bold">فهرست هزینه‌ها</h2>
        <div className="mb-4 grid gap-3 md:grid-cols-3">
          <Field label="از تاریخ">{(p) => <JalaliDateInput {...p} value={from} onChange={setFrom} max={to} />}</Field>
          <Field label="تا تاریخ">{(p) => <JalaliDateInput {...p} value={to} onChange={setTo} max={todayApiDate()} />}</Field>
          <Field label="دسته">
            {(p) => (
              <Select {...p} value={category} onChange={(e) => setCategory(e.target.value as ExpenseCategory | '')}>
                <option value="">همه</option>
                {expenseCategories.map((c) => (
                  <option key={c} value={c}>
                    {expenseCategoryLabel[c]}
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
          <EmptyState title="هزینه‌ای در این بازه ثبت نشده است" />
        ) : (
          <>
            <p className="mb-2 text-sm">
              جمع هزینه‌های معتبر: <b className="tabular-nums">{formatToman(total)}</b>
            </p>
            <ul className="divide-y">
              {list.data.items.map((e) => (
                <li key={e.id} className="flex flex-wrap items-center justify-between gap-2 py-3">
                  <div className={e.voided_at ? 'text-muted-foreground' : undefined}>
                    <p className="font-medium">
                      <Badge className="me-2">{expenseCategoryLabel[e.category]}</Badge>
                      {e.description}
                    </p>
                    <p className="mt-1 text-sm text-muted-foreground">{formatApiDate(e.expense_date)}</p>
                    {e.voided_at && <p className="text-sm text-danger">باطل شده: {e.void_reason}</p>}
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="font-bold tabular-nums">{formatToman(e.amount_rial)}</span>
                    {e.voided_at ? (
                      <Badge tone="danger">باطل</Badge>
                    ) : (
                      <Button variant="outline" onClick={() => setVoiding(e)}>
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
        title="ابطال هزینه"
        description="هزینه حذف نمی‌شود؛ باطل‌شده علامت می‌خورد و از گزارش‌ها کنار می‌رود."
        confirmLabel="ابطال هزینه"
        destructive
        reason={{ label: 'دلیل ابطال', required: true, max: 200 }}
        pending={voidIt.isPending}
        error={voidIt.error}
        onConfirm={(r) => voidIt.mutate(r)}
      />
    </div>
  )
}

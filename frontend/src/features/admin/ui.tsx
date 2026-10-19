import { useCallback, useId, useMemo, useRef, useState, type ComponentProps, type ReactNode } from 'react'

import { Dialog } from '@/components/Dialog'
import { Button } from '@/components/ui/button'
import { faDigits, toLatinDigits } from '@/lib/digits'
import { apiDateToJalali, daysInJalaliMonth, jalaliMonthNames, jalaliToApiDate, todayApiDate } from '@/lib/jalali'
import { formatTomanNumber, parseTomanToRial } from '@/lib/money'
import { cn } from '@/lib/utils'

import { formError } from './format'
import { ToastContext, type ToastTone } from './hooks'

/** Small building blocks shared by the staff screens. */

export const inputClass =
  'h-11 w-full rounded-lg border bg-surface px-3 text-base focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none disabled:opacity-60 aria-invalid:border-danger'

export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-2xl font-extrabold">{title}</h1>
        {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  )
}

export function Card({ className, ...props }: ComponentProps<'section'>) {
  return <section className={cn('rounded-xl border border-border/70 bg-surface p-4', className)} {...props} />
}

type Tone = 'neutral' | 'primary' | 'success' | 'warning' | 'danger'
const toneClass: Record<Tone, string> = {
  neutral: 'bg-muted text-foreground',
  primary: 'bg-primary/10 text-primary',
  success: 'bg-success/10 text-success',
  warning: 'bg-warning/10 text-warning',
  danger: 'bg-danger/10 text-danger',
}

export function Badge({ tone = 'neutral', children, className }: { tone?: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={cn('inline-flex items-center rounded-full px-2.5 py-0.5 text-sm font-medium whitespace-nowrap', toneClass[tone], className)}>
      {children}
    </span>
  )
}

/** Label + control + error/hint. The control gets the generated id via render prop. */
export function Field({
  label,
  error,
  hint,
  className,
  children,
}: {
  label: string
  error?: string
  hint?: string
  className?: string
  children: (props: { id: string; 'aria-invalid'?: boolean; 'aria-describedby'?: string }) => ReactNode
}) {
  const id = useId()
  const describedBy = error ? `${id}-err` : hint ? `${id}-hint` : undefined
  return (
    <div className={cn('space-y-1', className)}>
      <label htmlFor={id} className="block text-sm font-medium">
        {label}
      </label>
      {children({ id, 'aria-invalid': error ? true : undefined, 'aria-describedby': describedBy })}
      {error ? (
        <p id={`${id}-err`} className="text-sm text-danger">
          {error}
        </p>
      ) : (
        hint && (
          <p id={`${id}-hint`} className="text-sm text-muted-foreground">
            {hint}
          </p>
        )
      )}
    </div>
  )
}

/** Whole-number input that accepts Persian or Latin digits. */
export function NumberInput({
  value,
  onChange,
  className,
  ...props
}: Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'type'> & { value: string; onChange: (v: string) => void }) {
  return (
    <input
      {...props}
      inputMode="numeric"
      dir="ltr"
      value={value}
      onChange={(e) => onChange(toLatinDigits(e.target.value).replace(/[^\d]/g, ''))}
      className={cn(inputClass, 'text-end tabular-nums', className)}
    />
  )
}

/** Toman amount typed by staff; `value` is the raw text, parse it with parseTomanToRial. */
export function MoneyInput({
  value,
  onChange,
  className,
  ...props
}: Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'type'> & { value: string; onChange: (v: string) => void }) {
  const rial = parseTomanToRial(value)
  // The digits are typed left-to-right (dir=ltr, right-aligned); the unit sits at the field's
  // visual end in the RTL page.
  return (
    <div>
      <div className="relative">
        <input
          {...props}
          inputMode="numeric"
          dir="ltr"
          value={value}
          onChange={(e) => onChange(toLatinDigits(e.target.value).replace(/[^\d]/g, ''))}
          className={cn(inputClass, 'ps-16 text-end tabular-nums', className)}
        />
        <span className="pointer-events-none absolute inset-y-0 end-3 flex items-center text-sm text-muted-foreground" aria-hidden>
          تومان
        </span>
      </div>
      {rial !== null && rial > 0 && <p className="mt-1 text-xs text-muted-foreground tabular-nums">{formatTomanNumber(rial)} تومان</p>}
    </div>
  )
}


export function Select({ className, ...props }: ComponentProps<'select'>) {
  return <select {...props} className={cn(inputClass, 'appearance-auto pe-3', className)} />
}

export function TextArea({ className, ...props }: ComponentProps<'textarea'>) {
  return <textarea {...props} className={cn(inputClass, 'h-auto min-h-20 py-2', className)} />
}

/**
 * Jalali date picker made of three selects (day / month / year). `value` and `onChange` use API
 * dates (`YYYY-MM-DD`); `max` blocks future dates where the API rejects them.
 */
export function JalaliDateInput({
  id,
  value,
  onChange,
  max,
  'aria-invalid': invalid,
  'aria-describedby': describedBy,
}: {
  id?: string
  value: string
  onChange: (apiDate: string) => void
  max?: string
  'aria-invalid'?: boolean
  'aria-describedby'?: string
}) {
  const j = apiDateToJalali(value)
  const thisYear = apiDateToJalali(todayApiDate()).year
  const years = [thisYear - 2, thisYear - 1, thisYear, thisYear + 1].filter((y) => !max || y <= apiDateToJalali(max).year)
  const set = (year: number, month: number, day: number) => {
    let next = jalaliToApiDate(year, month, Math.min(day, daysInJalaliMonth(year, month)))
    if (max && next > max) next = max
    onChange(next)
  }
  const common = { 'aria-invalid': invalid, 'aria-describedby': describedBy, className: cn(inputClass, 'appearance-auto px-2') }
  return (
    <div className="grid grid-cols-[1fr_1.6fr_1.3fr] gap-2" role="group">
      <select id={id} aria-label="روز" value={j.day} onChange={(e) => set(j.year, j.month, Number(e.target.value))} {...common}>
        {Array.from({ length: daysInJalaliMonth(j.year, j.month) }, (_, i) => i + 1).map((d) => (
          <option key={d} value={d}>
            {faDigits(d)}
          </option>
        ))}
      </select>
      <select aria-label="ماه" value={j.month} onChange={(e) => set(j.year, Number(e.target.value), j.day)} {...common}>
        {jalaliMonthNames.map((name, i) => (
          <option key={name} value={i + 1}>
            {name}
          </option>
        ))}
      </select>
      <select aria-label="سال" value={j.year} onChange={(e) => set(Number(e.target.value), j.month, j.day)} {...common}>
        {years.map((y) => (
          <option key={y} value={y}>
            {faDigits(y)}
          </option>
        ))}
      </select>
    </div>
  )
}

/** Accessible on/off switch (role="switch"), 44 px tall touch target. */
export function Switch({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean
  onChange: (next: boolean) => void
  label: string
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className="group inline-flex h-11 shrink-0 items-center px-1 disabled:opacity-50"
    >
      <span
        className={cn(
          'relative inline-flex h-7 w-12 items-center rounded-full transition-colors group-focus-visible:ring-[3px] group-focus-visible:ring-ring/50',
          checked ? 'bg-success' : 'bg-border',
        )}
      >
        <span
          className={cn(
            'absolute size-5 rounded-full bg-white shadow transition-[inset-inline-start]',
            checked ? '[inset-inline-start:1.5rem]' : '[inset-inline-start:0.25rem]',
          )}
        />
      </span>
    </button>
  )
}


export function FormError({ error }: { error: unknown }) {
  if (!error) return null
  return (
    <p role="alert" className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
      {formError(error)}
    </p>
  )
}



// ---- toasts ----

interface Toast {
  id: number
  text: string
  tone: ToastTone
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const seq = useRef(0)
  const push = useCallback((text: string, tone: ToastTone = 'success') => {
    const id = ++seq.current
    setToasts((t) => [...t, { id, text, tone }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 4000)
  }, [])
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div aria-live="polite" className="pointer-events-none fixed inset-x-0 bottom-20 z-50 flex flex-col items-center gap-2 px-4 lg:bottom-6">
        {toasts.map((t) => (
          <p
            key={t.id}
            role="status"
            className={cn(
              'rounded-xl px-4 py-3 text-sm font-medium shadow-lg animate-in fade-in slide-in-from-bottom-2',
              t.tone === 'success' ? 'bg-foreground text-background' : 'bg-danger text-white',
            )}
          >
            {t.text}
          </p>
        ))}
      </div>
    </ToastContext.Provider>
  )
}


/** Confirmation with an optional required reason (voids, refunds, cancellations). */
export function ConfirmDialog({
  open,
  onClose,
  title,
  description,
  confirmLabel,
  destructive,
  reason,
  pending,
  error,
  onConfirm,
  children,
}: {
  open: boolean
  onClose: () => void
  title: string
  description?: ReactNode
  confirmLabel: string
  destructive?: boolean
  /** Ask for a reason; `required` blocks confirming without one. */
  reason?: { label: string; required?: boolean; max?: number }
  pending?: boolean
  error?: unknown
  onConfirm: (reason: string) => void
  children?: ReactNode
}) {
  const [text, setText] = useState('')
  const blocked = useMemo(() => !!reason?.required && text.trim() === '', [reason, text])
  const close = () => {
    setText('')
    onClose()
  }
  return (
    <Dialog
      open={open}
      onClose={close}
      title={title}
      footer={
        <>
          <Button
            className="flex-1"
            variant={destructive ? 'destructive' : 'default'}
            disabled={pending || blocked}
            onClick={() => onConfirm(text.trim())}
          >
            {pending ? 'در حال ثبت…' : confirmLabel}
          </Button>
          <Button variant="outline" className="flex-1" onClick={close}>
            انصراف
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {description && <div className="text-muted-foreground">{description}</div>}
        {children}
        {reason && (
          <Field label={reason.label}>
            {(p) => <TextArea {...p} value={text} maxLength={reason.max ?? 200} onChange={(e) => setText(e.target.value)} />}
          </Field>
        )}
        <FormError error={error} />
      </div>
    </Dialog>
  )
}

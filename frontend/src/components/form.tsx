import { useId, type ReactNode } from 'react'

import { errorMessageFa } from '@/lib/errors-fa'
import { cn } from '@/lib/utils'

/** Form primitives shared by the customer app (the staff app keeps its own in features/admin/ui). */

export const inputClass =
  'h-12 w-full rounded-lg border bg-surface px-3 text-base focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none disabled:opacity-60 aria-invalid:border-danger'

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

/** A request error as one Persian sentence; `message` overrides the generic text. */
export function FormError({ error, message }: { error: unknown; message?: string }) {
  if (!error) return null
  return (
    <p role="alert" className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
      {message ?? errorMessageFa(error)}
    </p>
  )
}

export function Notice({ tone = 'info', children, className }: { tone?: 'info' | 'warning' | 'success'; children: ReactNode; className?: string }) {
  return (
    <div
      className={cn(
        'rounded-lg px-3 py-2 text-sm',
        tone === 'info' && 'bg-primary/10 text-foreground',
        tone === 'warning' && 'bg-warning/10 text-foreground',
        tone === 'success' && 'bg-success/10 text-foreground',
        className,
      )}
    >
      {children}
    </div>
  )
}

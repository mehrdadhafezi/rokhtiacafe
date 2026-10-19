import type { ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { errorMessageFa } from '@/lib/errors-fa'
import { cn } from '@/lib/utils'

/** Shared loading / empty / error states, used by every screen in both apps. */

export function LoadingState({ label = 'در حال بارگذاری…', className }: { label?: string; className?: string }) {
  return (
    <div role="status" aria-live="polite" className={cn('flex flex-col items-center gap-3 py-12 text-muted-foreground', className)}>
      <span className="size-8 animate-spin rounded-full border-4 border-muted border-t-primary" aria-hidden />
      <span>{label}</span>
    </div>
  )
}

export function EmptyState({
  title,
  description,
  action,
  className,
}: {
  title: string
  description?: string
  action?: ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col items-center gap-2 rounded-lg bg-surface px-6 py-12 text-center', className)}>
      <p className="text-lg font-semibold">{title}</p>
      {description && <p className="text-muted-foreground">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

export function ErrorState({
  error,
  title = 'مشکلی پیش آمد',
  onRetry,
  className,
}: {
  error?: unknown
  title?: string
  onRetry?: () => void
  className?: string
}) {
  return (
    <div
      role="alert"
      className={cn('flex flex-col items-center gap-2 rounded-lg border border-danger/30 bg-surface px-6 py-12 text-center', className)}
    >
      <p className="text-lg font-semibold text-danger">{title}</p>
      <p className="text-muted-foreground">{errorMessageFa(error)}</p>
      {onRetry && (
        <Button variant="outline" className="mt-2" onClick={onRetry}>
          تلاش دوباره
        </Button>
      )}
    </div>
  )
}

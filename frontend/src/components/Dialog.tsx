import { useEffect, useId, useRef, type ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * Modal built on the native <dialog> element: focus trapping, Escape to close and the inert
 * background come from the browser. Rendered as a bottom sheet on phones.
 */
export function Dialog({
  open,
  onClose,
  title,
  children,
  footer,
  className,
}: {
  open: boolean
  onClose: () => void
  title: string
  children: ReactNode
  footer?: ReactNode
  className?: string
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()

  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) d.showModal()
    if (!open && d.open) d.close()
  }, [open])

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onClose={onClose}
      onClick={(e) => e.target === ref.current && onClose()}
      className={cn(
        'm-0 mt-auto w-full max-w-none rounded-t-2xl bg-surface p-0 text-foreground shadow-xl backdrop:bg-foreground/40',
        'sm:m-auto sm:max-w-md sm:rounded-2xl',
        'open:animate-in open:fade-in open:slide-in-from-bottom-8',
        className,
      )}
    >
      <div className="flex max-h-[85dvh] flex-col">
        <h2 id={titleId} className="border-b px-5 py-4 text-lg font-bold">
          {title}
        </h2>
        <div className="overflow-y-auto px-5 py-4">{children}</div>
        {footer && <div className="flex gap-3 border-t px-5 py-4 pb-[max(1rem,env(safe-area-inset-bottom))]">{footer}</div>}
      </div>
    </dialog>
  )
}

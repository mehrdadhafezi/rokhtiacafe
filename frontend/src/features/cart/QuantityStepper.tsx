import { MinusIcon, PlusIcon, TrashIcon } from '@/components/icons'
import { faDigits } from '@/lib/digits'
import { cn } from '@/lib/utils'

import { MAX_QUANTITY } from './cart'

/** − n + control with 44 px touch targets. At quantity 1 the minus button removes the item. */
export function QuantityStepper({
  quantity,
  name,
  onIncrement,
  onDecrement,
  className,
}: {
  quantity: number
  name: string
  onIncrement: () => void
  onDecrement: () => void
  className?: string
}) {
  return (
    <div className={cn('inline-flex items-center rounded-full bg-primary text-primary-foreground shadow-sm', className)}>
      <button
        type="button"
        onClick={onIncrement}
        disabled={quantity >= MAX_QUANTITY}
        aria-label={`افزودن یک ${name}`}
        className="grid size-11 place-items-center rounded-full transition hover:bg-white/10 focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-40"
      >
        <PlusIcon />
      </button>
      <span className="min-w-6 text-center text-base font-bold tabular-nums" aria-live="polite" aria-label={`تعداد ${name}`}>
        {faDigits(quantity)}
      </span>
      <button
        type="button"
        onClick={onDecrement}
        aria-label={quantity === 1 ? `حذف ${name}` : `کم کردن یک ${name}`}
        className="grid size-11 place-items-center rounded-full transition hover:bg-white/10 focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        {quantity === 1 ? <TrashIcon width={18} height={18} /> : <MinusIcon />}
      </button>
    </div>
  )
}

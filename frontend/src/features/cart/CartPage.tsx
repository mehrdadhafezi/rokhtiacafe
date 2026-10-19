import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router'

import { Dialog } from '@/components/Dialog'
import { ChevronIcon, TrashIcon } from '@/components/icons'
import { EmptyState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { createOrder, fetchMenu, menuQueryKey, orderQueryKey } from '@/features/menu/api'
import { faClock } from '@/features/menu/format'
import { ProductImage } from '@/features/menu/ProductImage'
import { isApiError } from '@/lib/api'
import { errorMessageFa } from '@/lib/errors-fa'
import { faDigits } from '@/lib/digits'
import { formatToman } from '@/lib/money'
import { cn } from '@/lib/utils'

import { cartCount, cartReducer, cartTotalRial, reconcileWithMenu, requestFingerprint, toOrderBody, type Cart, type PriceChange } from './cart'
import { useCart } from './CartContext'
import { QuantityStepper } from './QuantityStepper'
import { checkoutKey, clearCheckoutKey, clientId, rememberOrder } from './storage'

function PriceChangeList({ changes }: { changes: PriceChange[] }) {
  return (
    <ul className="space-y-2">
      {changes.map((c) => (
        <li key={c.productId} className="flex items-center justify-between gap-3 rounded-lg bg-muted/60 px-3 py-2">
          <span className="font-medium">{c.name}</span>
          <span className="text-sm tabular-nums">
            <s className="text-muted-foreground">{formatToman(c.oldRial)}</s>
            <span className="mx-1" aria-hidden>
              ←
            </span>
            <b className={c.newRial > c.oldRial ? 'text-danger' : 'text-success'}>{formatToman(c.newRial)}</b>
          </span>
        </li>
      ))}
    </ul>
  )
}

/** /menu/cart — review, price confirmation and checkout (PRD §5.1 C2–C4, C7; F13). */
export function CartPage() {
  const { cart, dispatch } = useCart()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const menu = useQuery({ queryKey: menuQueryKey, queryFn: ({ signal }) => fetchMenu(signal), staleTime: 30_000 })

  // Results the server reported on the last submit; everything else is derived from the menu.
  const [serverPrices, setServerPrices] = useState<PriceChange[]>([])
  const [serverUnavailable, setServerUnavailable] = useState<Set<number>>(new Set())
  const [shortStock, setShortStock] = useState<Set<number>>(new Set())
  // The error of the last submit, keyed by its request: it disappears once the cart changes.
  const [submitError, setSubmitError] = useState<{ fp: string; message: string } | null>(null)
  const [dialogDismissed, setDialogDismissed] = useState(false)

  // The stored cart is compared with the live menu on every render: prices that differ are
  // shown old → new and change only after the customer confirms (F13).
  const recon = menu.data ? reconcileWithMenu(cart, menu.data) : null
  const pendingPrices = serverPrices.length > 0 ? serverPrices : (recon?.priceChanges ?? [])
  const unavailable = new Set([...(recon?.unavailable.map((u) => u.productId) ?? []), ...serverUnavailable])

  const submit = useMutation({
    mutationFn: (c: Cart) => createOrder(toOrderBody(c), checkoutKey(requestFingerprint(c)), clientId()),
    onMutate: () => setSubmitError(null),
    onSuccess: (order) => {
      if (!order.tracking_token) return
      rememberOrder({ token: order.tracking_token, number: order.order_number, createdAt: order.created_at })
      queryClient.setQueryData(orderQueryKey(order.tracking_token), order)
      clearCheckoutKey()
      dispatch({ type: 'clearItems' })
      navigate(`/menu/order#${order.tracking_token}`, { replace: true })
    },
    onError: (err, submitted) => {
      if (isApiError(err, 'PRICE_CHANGED')) {
        const items = (err.details.items as { product_id: number; current_unit_price_rial: number }[] | undefined) ?? []
        void queryClient.invalidateQueries({ queryKey: menuQueryKey })
        setDialogDismissed(false)
        setServerPrices(
          items.flatMap((i) => {
            const line = cart.lines.find((l) => l.productId === i.product_id)
            return line ? [{ productId: line.productId, name: line.name, oldRial: line.unitPriceRial, newRial: i.current_unit_price_rial }] : []
          }),
        )
        return
      }
      const ids = new Set(((err as { details?: { product_ids?: number[] } }).details?.product_ids ?? []) as number[])
      if (isApiError(err, 'INSUFFICIENT_STOCK')) {
        setShortStock(ids)
        void queryClient.invalidateQueries({ queryKey: menuQueryKey })
      } else if (isApiError(err, 'PRODUCT_UNAVAILABLE')) {
        setServerUnavailable(ids)
        void queryClient.invalidateQueries({ queryKey: menuQueryKey })
      }
      setSubmitError({ fp: requestFingerprint(submitted), message: errorMessageFa(err) })
    },
  })

  const confirmPrices = () => {
    const prices = Object.fromEntries(pendingPrices.map((p) => [p.productId, p.newRial]))
    const next = cartReducer(cart, { type: 'applyPrices', prices })
    dispatch({ type: 'applyPrices', prices })
    const fromServer = serverPrices.length > 0
    setServerPrices([])
    if (fromServer) submit.mutate(next) // the customer was submitting: continue with the new prices
  }

  if (cartCount(cart) === 0) {
    return (
      <EmptyState
        title="سبد خرید خالی است"
        description="از منو چیزی انتخاب کنید."
        action={
          <Button asChild>
            <Link to="/menu">مشاهدهٔ منو</Link>
          </Button>
        }
      />
    )
  }

  const ordering = menu.data?.ordering
  const blocked = unavailable.size > 0 || pendingPrices.length > 0
  const canSubmit = !submit.isPending && !blocked && ordering?.open !== false

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Link to="/menu" aria-label="بازگشت به منو" className="grid size-11 place-items-center rounded-full hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50">
          <ChevronIcon />
        </Link>
        <h1 className="text-xl font-extrabold">سبد خرید</h1>
      </div>

      <ul className="space-y-3" aria-label="اقلام سبد">
        {cart.lines.map((l) => {
          const gone = unavailable.has(l.productId)
          const short = shortStock.has(l.productId)
          return (
            <li key={l.productId} className={cn('rounded-xl border bg-surface p-3', gone || short ? 'border-danger/40' : 'border-border/70')}>
              <div className="flex gap-3">
                <ProductImage src={l.imageUrl} alt={l.name} className="size-16 shrink-0 rounded-lg" />
                <div className="min-w-0 flex-1">
                  <p className="font-bold">{l.name}</p>
                  <p className="text-sm text-muted-foreground tabular-nums">{formatToman(l.unitPriceRial)}</p>
                  {gone && <p className="mt-1 text-sm font-medium text-danger">این محصول فعلاً موجود نیست.</p>}
                  {short && !gone && <p className="mt-1 text-sm font-medium text-danger">موجودی به این تعداد کافی نیست؛ تعداد را کم کنید.</p>}
                </div>
              </div>
              <div className="mt-2 flex items-center justify-between">
                {gone ? (
                  <Button variant="outline" size="sm" onClick={() => dispatch({ type: 'remove', productId: l.productId })} aria-label={`حذف ${l.name} از سبد`}>
                    <TrashIcon /> حذف از سبد
                  </Button>
                ) : (
                  <QuantityStepper
                    quantity={l.quantity}
                    name={l.name}
                    onIncrement={() => dispatch({ type: 'increment', productId: l.productId })}
                    onDecrement={() => {
                      dispatch({ type: 'decrement', productId: l.productId })
                      setShortStock((s) => {
                        const n = new Set(s)
                        n.delete(l.productId)
                        return n
                      })
                    }}
                  />
                )}
                <p className="font-extrabold tabular-nums">{formatToman(l.unitPriceRial * l.quantity)}</p>
              </div>
            </li>
          )
        })}
      </ul>

      {pendingPrices.length > 0 && dialogDismissed && (
        <div role="alert" className="flex items-center justify-between gap-3 rounded-lg bg-warning/10 px-4 py-3 text-sm text-warning">
          <span>قیمت برخی اقلام تغییر کرده است.</span>
          <Button variant="outline" size="sm" onClick={() => setDialogDismissed(false)}>
            بررسی قیمت‌ها
          </Button>
        </div>
      )}

      {unavailable.size > 0 && (
        <div role="alert" className="flex items-center justify-between gap-3 rounded-lg bg-danger/10 px-4 py-3 text-sm text-danger">
          <span>برخی اقلام موجود نیستند. برای ادامه آن‌ها را حذف کنید.</span>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              dispatch({ type: 'removeMany', productIds: [...unavailable] })
              setServerUnavailable(new Set())
            }}
          >
            حذف همه
          </Button>
        </div>
      )}

      <section className="space-y-3 rounded-xl border border-border/70 bg-surface p-4">
        <div>
          <label htmlFor="label" className="mb-1 block text-sm font-medium">
            نام یا نشانه <span className="text-muted-foreground">(اختیاری)</span>
          </label>
          <input
            id="label"
            value={cart.label}
            maxLength={40}
            autoComplete="given-name"
            onChange={(e) => dispatch({ type: 'setLabel', label: e.target.value })}
            placeholder="مثلاً سارا یا میز پنجره"
            className="h-11 w-full rounded-lg border bg-background px-3 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
          />
        </div>
        <div>
          <label htmlFor="note" className="mb-1 block text-sm font-medium">
            توضیحات <span className="text-muted-foreground">(اختیاری)</span>
          </label>
          <textarea
            id="note"
            value={cart.note}
            maxLength={200}
            rows={2}
            onChange={(e) => dispatch({ type: 'setNote', note: e.target.value })}
            placeholder="مثلاً بدون شکر"
            className="w-full resize-none rounded-lg border bg-background px-3 py-2 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
          />
        </div>
      </section>

      <div className="fixed inset-x-0 bottom-0 z-20 mx-auto max-w-lg border-t border-border/70 bg-background/95 px-4 pt-3 pb-[max(1rem,env(safe-area-inset-bottom))] backdrop-blur-md">
        {submitError && submitError.fp === requestFingerprint(cart) && (
          <p role="alert" className="mb-2 rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
            {submitError.message}
          </p>
        )}
        {ordering && !ordering.open && (
          <p role="status" className="mb-2 rounded-lg bg-warning/10 px-3 py-2 text-sm text-warning">
            سفارش‌گیری فعلاً بسته است. از ساعت {faClock(ordering.opens_at)} دوباره امتحان کنید.
          </p>
        )}
        <div className="mb-3 flex items-center justify-between">
          <span className="text-muted-foreground">
            جمع ({faDigits(cartCount(cart))} قلم) · پرداخت در کافه
          </span>
          <span className="text-lg font-extrabold text-primary tabular-nums">{formatToman(cartTotalRial(cart))}</span>
        </div>
        <Button size="lg" className="w-full rounded-xl" disabled={!canSubmit} onClick={() => submit.mutate(cart)}>
          {submit.isPending ? 'در حال ثبت سفارش…' : 'ثبت سفارش'}
        </Button>
      </div>

      <Dialog
        open={pendingPrices.length > 0 && !dialogDismissed}
        onClose={() => setDialogDismissed(true)}
        title="قیمت برخی اقلام تغییر کرده است"
        footer={
          <>
            <Button className="flex-1" onClick={confirmPrices}>
              تأیید قیمت‌های جدید
            </Button>
            <Button variant="outline" className="flex-1" onClick={() => navigate('/menu')}>
              بازگشت به منو
            </Button>
          </>
        }
      >
        <p className="mb-3 text-muted-foreground">پیش از ثبت سفارش، قیمت‌های جدید را بررسی و تأیید کنید.</p>
        <PriceChangeList changes={pendingPrices} />
      </Dialog>
    </div>
  )
}

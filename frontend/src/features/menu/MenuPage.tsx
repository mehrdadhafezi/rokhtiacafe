import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router'

import { ChevronIcon, ClockIcon, PlusIcon } from '@/components/icons'
import { EmptyState, ErrorState } from '@/components/states'
import { useCart } from '@/features/cart/CartContext'
import { cartCount, cartTotalRial, quantityOf } from '@/features/cart/cart'
import { QuantityStepper } from '@/features/cart/QuantityStepper'
import { recentOrders } from '@/features/cart/storage'
import { faDigits } from '@/lib/digits'
import { formatToman, formatTomanNumber } from '@/lib/money'
import { cn } from '@/lib/utils'

import { fetchMenu, menuQueryKey, type MenuCategory, type MenuItem, type Ordering } from './api'
import { faClock } from './format'
import { ProductImage } from './ProductImage'

function OrderingBanner({ ordering }: { ordering: Ordering }) {
  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-lg px-3 py-2 text-sm',
        ordering.open ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning',
      )}
    >
      <ClockIcon width={18} height={18} />
      {ordering.open ? (
        <span>
          سفارش‌گیری باز است · تا ساعت <b className="tabular-nums">{faClock(ordering.closes_at)}</b>
        </span>
      ) : (
        <span>
          سفارش‌گیری فعلاً بسته است. از ساعت <b className="tabular-nums">{faClock(ordering.opens_at)}</b> می‌توانید سفارش دهید.
        </span>
      )}
    </div>
  )
}

function ActiveOrders() {
  const [orders] = useState(() => recentOrders())
  if (orders.length === 0) return null
  return (
    <div className="space-y-2">
      {orders.slice(0, 2).map((o) => (
        <Link
          key={o.token}
          to={`/menu/order#${o.token}`}
          className="flex items-center justify-between rounded-lg border border-primary/20 bg-primary/5 px-4 py-3 text-primary transition hover:bg-primary/10 focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <span>
            پیگیری سفارش <b className="tabular-nums">#{faDigits(o.number)}</b>
          </span>
          <ChevronIcon className="rotate-180" />
        </Link>
      ))}
    </div>
  )
}

function ProductCard({ item, ordering }: { item: MenuItem; ordering: Ordering }) {
  const { cart, dispatch } = useCart()
  const qty = quantityOf(cart, item.id)
  const add = () =>
    dispatch({ type: 'add', item: { productId: item.id, name: item.name, unitPriceRial: item.price_rial, imageUrl: item.image_url } })
  const soldOut = !item.available

  return (
    <article
      className={cn(
        'flex gap-3 rounded-xl border border-border/70 bg-surface p-3 shadow-[0_1px_2px_rgb(43_30_22/0.05)] transition',
        soldOut && 'opacity-60',
      )}
    >
      <ProductImage src={item.image_url} alt={item.name} className="size-24 shrink-0 rounded-lg" />
      <div className="flex min-w-0 flex-1 flex-col">
        <h3 className="font-bold leading-snug">{item.name}</h3>
        {item.description && <p className="mt-0.5 line-clamp-2 text-sm text-muted-foreground">{item.description}</p>}
        <div className="mt-auto flex flex-wrap items-end justify-between gap-2 pt-2">
          <p className="whitespace-nowrap text-base font-extrabold text-primary tabular-nums">
            {formatTomanNumber(item.price_rial)} <span className="text-xs font-medium text-muted-foreground">تومان</span>
          </p>
          {soldOut ? (
            <span className="rounded-full bg-muted px-3 py-1 text-sm font-medium text-muted-foreground">تمام شد</span>
          ) : qty > 0 ? (
            <QuantityStepper
              className="ms-auto"
              quantity={qty}
              name={item.name}
              onIncrement={() => dispatch({ type: 'increment', productId: item.id })}
              onDecrement={() => dispatch({ type: 'decrement', productId: item.id })}
            />
          ) : (
            <button
              type="button"
              onClick={add}
              disabled={!ordering.open}
              aria-label={`افزودن ${item.name} به سبد`}
              className="grid size-11 place-items-center rounded-full bg-primary text-primary-foreground shadow-sm transition hover:bg-primary/90 focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-95 disabled:bg-muted disabled:text-muted-foreground"
            >
              <PlusIcon />
            </button>
          )}
        </div>
      </div>
    </article>
  )
}

function CategoryNav({ categories, active, onSelect }: { categories: MenuCategory[]; active: number; onSelect: (id: number) => void }) {
  return (
    <nav aria-label="دسته‌بندی‌ها" className="sticky top-[calc(3.5rem+env(safe-area-inset-top))] z-10 -mx-4 bg-background/90 px-4 py-2 backdrop-blur-md">
      <ul className="flex gap-2 overflow-x-auto [scrollbar-width:none]">
        {categories.map((c) => (
          <li key={c.id} className="shrink-0">
            <button
              type="button"
              onClick={() => onSelect(c.id)}
              aria-current={active === c.id ? 'true' : undefined}
              className={cn(
                'h-11 shrink-0 whitespace-nowrap rounded-full border px-4 text-sm font-medium transition focus-visible:ring-[3px] focus-visible:ring-ring/50',
                active === c.id ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-surface hover:bg-muted',
              )}
            >
              {c.name}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  )
}

function MenuSkeleton() {
  return (
    <div role="status" aria-label="در حال بارگذاری منو" className="space-y-3">
      <div className="h-28 animate-pulse rounded-2xl bg-muted" />
      <div className="h-10 animate-pulse rounded-lg bg-muted" />
      {Array.from({ length: 4 }, (_, i) => (
        <div key={i} className="flex gap-3 rounded-xl border border-border/50 bg-surface p-3">
          <div className="size-24 animate-pulse rounded-lg bg-muted" />
          <div className="flex-1 space-y-2 pt-1">
            <div className="h-4 w-1/2 animate-pulse rounded bg-muted" />
            <div className="h-3 w-3/4 animate-pulse rounded bg-muted" />
            <div className="h-4 w-1/3 animate-pulse rounded bg-muted" />
          </div>
        </div>
      ))}
    </div>
  )
}

function CartBar() {
  const { cart } = useCart()
  const count = cartCount(cart)
  if (count === 0) return null
  return (
    <div className="fixed inset-x-0 bottom-0 z-20 mx-auto max-w-lg px-4 pb-[max(1rem,env(safe-area-inset-bottom))] animate-in fade-in slide-in-from-bottom-4">
      <Link
        to="/menu/cart"
        className="flex h-14 items-center justify-between gap-3 rounded-2xl bg-primary px-5 text-primary-foreground shadow-lg shadow-primary/30 transition hover:bg-primary/95 focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-[0.99]"
      >
        <span className="flex items-center gap-2 whitespace-nowrap font-bold">
          <span className="grid min-w-7 place-items-center rounded-full bg-white/20 px-2 py-0.5 text-sm tabular-nums">{faDigits(count)}</span>
          مشاهده سبد
        </span>
        <span className="whitespace-nowrap font-extrabold tabular-nums">{formatToman(cartTotalRial(cart))}</span>
      </Link>
    </div>
  )
}

/** /menu — categories, products and the cart bar (PRD §5.1 C1–C2). */
export function MenuPage() {
  const { data, error, isPending, refetch } = useQuery({
    queryKey: menuQueryKey,
    queryFn: ({ signal }) => fetchMenu(signal),
    staleTime: 30_000,
  })
  const [active, setActive] = useState<number | null>(null)
  const sections = useRef(new Map<number, HTMLElement>())
  const categories = useMemo(() => data?.categories ?? [], [data])

  // Highlight the category whose section is near the top of the screen. The last sections can
  // never reach the top, so at the bottom of the page the last category wins.
  useEffect(() => {
    if (categories.length === 0) return
    const lastId = categories[categories.length - 1]!.id
    const atBottom = () => window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4
    const observer = new IntersectionObserver(
      (entries) => {
        if (atBottom()) return setActive(lastId)
        const visible = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        const id = Number(visible[0]?.target.getAttribute('data-category'))
        if (id) setActive(id)
      },
      { rootMargin: '-120px 0px -60% 0px' },
    )
    const onScroll = () => {
      if (atBottom()) setActive(lastId)
    }
    sections.current.forEach((el) => observer.observe(el))
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => {
      observer.disconnect()
      window.removeEventListener('scroll', onScroll)
    }
  }, [categories])

  const jump = (id: number) => {
    setActive(id)
    sections.current.get(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  if (isPending) return <MenuSkeleton />
  if (error) return <ErrorState error={error} title="منو بارگذاری نشد" onRetry={() => void refetch()} />

  return (
    <div className="space-y-4">
      <section className="rounded-2xl bg-gradient-to-bl from-primary to-[#4a2c19] px-5 py-6 text-primary-foreground shadow-md">
        <p className="text-sm opacity-80">به کافه رخ تیا خوش آمدید</p>
        <h2 className="mt-1 text-2xl font-extrabold">منوی امروز</h2>
        <p className="mt-2 text-sm opacity-80">انتخاب کنید، سفارش دهید و با شمارهٔ سفارش آن را دنبال کنید.</p>
      </section>
      <OrderingBanner ordering={data.ordering} />
      <ActiveOrders />

      {categories.length === 0 ? (
        <EmptyState title="منو هنوز آماده نیست" description="به‌زودی محصولات کافه اینجا نمایش داده می‌شوند." />
      ) : (
        <>
          <CategoryNav categories={categories} active={active ?? categories[0]!.id} onSelect={jump} />
          {categories.map((c) => (
            <section
              key={c.id}
              data-category={c.id}
              ref={(el) => {
                if (el) sections.current.set(c.id, el)
                else sections.current.delete(c.id)
              }}
              aria-labelledby={`cat-${c.id}`}
              className="scroll-mt-32 space-y-3"
            >
              <h2 id={`cat-${c.id}`} className="pt-2 text-xl font-extrabold">
                {c.name}
              </h2>
              {c.products.map((p) => (
                <ProductCard key={p.id} item={p} ordering={data.ordering} />
              ))}
            </section>
          ))}
        </>
      )}
      <CartBar />
    </div>
  )
}

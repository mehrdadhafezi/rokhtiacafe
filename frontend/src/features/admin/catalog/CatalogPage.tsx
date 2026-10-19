import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import { Dialog } from '@/components/Dialog'
import { CupIcon } from '@/components/icons'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { faDigits } from '@/lib/digits'
import { formatToman, parseTomanToRial } from '@/lib/money'
import { cn } from '@/lib/utils'

import { catalogApi, type Category, type Product, type ProductBody } from '../api'
import { rialToInput } from '../format'
import { useAuth, useToast } from '../hooks'
import { categoriesKey, dashboardKey, inventoryKey, productsKey } from '../keys'
import { inventoryModeLabel, type InventoryMode } from '../labels'
import { Badge, Card, Field, FormError, MoneyInput, NumberInput, PageHeader, Select, Switch, TextArea, inputClass } from '../ui'
import { IMAGE_TYPES, imageFileProblem } from './image'

function useCatalog() {
  const categories = useQuery({ queryKey: categoriesKey, queryFn: ({ signal }) => catalogApi.categories(signal) })
  const products = useQuery({ queryKey: productsKey, queryFn: ({ signal }) => catalogApi.products(signal) })
  return { categories, products }
}

function useRefreshCatalog() {
  const queryClient = useQueryClient()
  return () => {
    for (const key of [categoriesKey, productsKey, inventoryKey, dashboardKey]) void queryClient.invalidateQueries({ queryKey: key })
  }
}

function ProductRow({ product, onEdit }: { product: Product; onEdit?: () => void }) {
  const { can } = useAuth()
  const toast = useToast()
  const refresh = useRefreshCatalog()
  const toggle = useMutation({
    mutationFn: (next: boolean) => catalogApi.setAvailability(product.id, next),
    onSuccess: (p) => {
      refresh()
      toast(p.is_available ? `${p.name} دوباره قابل سفارش است.` : `${p.name} «تمام شد» زده شد.`)
    },
    onError: () => toast('تغییر وضعیت انجام نشد.', 'danger'),
  })
  return (
    <li className={cn('flex items-center gap-3 py-3', !product.is_active && 'opacity-60')}>
      {product.image_url ? (
        <img src={product.image_url} alt="" className="size-14 shrink-0 rounded-lg object-cover" loading="lazy" />
      ) : (
        <span className="grid size-14 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground" aria-hidden>
          <CupIcon width={24} height={24} />
        </span>
      )}
      <div className="min-w-0 flex-1">
        <p className="font-bold">{product.name}</p>
        <p className="text-sm tabular-nums">{formatToman(product.price_rial)}</p>
        <div className="mt-1 flex flex-wrap gap-1">
          <Badge>{inventoryModeLabel[product.inventory_mode]}</Badge>
          {!product.is_active && <Badge tone="danger">غیرفعال</Badge>}
        </div>
      </div>
      {product.is_active && (
        <div className="flex flex-col items-center">
          <Switch
            label={`${product.name} قابل سفارش است`}
            checked={product.is_available}
            disabled={!can('catalog.availability') || toggle.isPending}
            onChange={(next) => toggle.mutate(next)}
          />
          <span className="text-xs text-muted-foreground">{product.is_available ? 'موجود' : 'تمام شد'}</span>
        </div>
      )}
      {onEdit && (
        <Button variant="outline" onClick={onEdit} aria-label={`ویرایش ${product.name}`}>
          ویرایش
        </Button>
      )}
    </li>
  )
}

interface ProductForm {
  category_id: string
  name: string
  description: string
  price: string
  inventory_mode: InventoryMode
  threshold: string
  estimate: string
  sort_order: string
  is_active: boolean
}

function toForm(p: Product | null, categories: Category[]): ProductForm {
  return {
    category_id: String(p?.category_id ?? categories[0]?.id ?? ''),
    name: p?.name ?? '',
    description: p?.description ?? '',
    price: rialToInput(p?.price_rial),
    inventory_mode: p?.inventory_mode ?? 'UNTRACKED',
    threshold: p?.low_stock_threshold != null ? String(p.low_stock_threshold) : '',
    estimate: rialToInput(p?.estimated_unit_cost_rial),
    sort_order: String(p?.sort_order ?? 0),
    is_active: p?.is_active ?? true,
  }
}

function ProductDialog({ product, categories, onClose }: { product: Product | null; categories: Category[]; onClose: () => void }) {
  const [f, setF] = useState<ProductForm>(() => toForm(product, categories))
  const set = <K extends keyof ProductForm>(k: K, v: ProductForm[K]) => setF((x) => ({ ...x, [k]: v }))
  const refresh = useRefreshCatalog()
  const toast = useToast()
  const [image, setImage] = useState<string | null>(product?.image_url ?? null)
  const [fileProblem, setFileProblem] = useState<string | null>(null)
  const price = parseTomanToRial(f.price)
  const tracked = f.inventory_mode === 'TRACKED'

  const body = (): ProductBody => ({
    category_id: Number(f.category_id),
    name: f.name.trim(),
    description: f.description.trim() || null,
    price_rial: price ?? 0,
    inventory_mode: f.inventory_mode,
    low_stock_threshold: tracked && f.threshold !== '' ? Number(f.threshold) : null,
    estimated_unit_cost_rial: !tracked && f.estimate !== '' ? parseTomanToRial(f.estimate) : null,
    sort_order: Number(f.sort_order || 0),
    ...(product ? { is_active: f.is_active } : {}),
  })
  const save = useMutation({
    mutationFn: () => (product ? catalogApi.updateProduct(product.id, body()) : catalogApi.createProduct(body())),
    onSuccess: (p) => {
      refresh()
      toast(product ? `${p.name} ذخیره شد.` : `${p.name} اضافه شد.`)
      onClose()
    },
  })
  const upload = useMutation({
    mutationFn: (file: File) => catalogApi.uploadImage(product!.id, file),
    onSuccess: (p) => {
      setImage(p.image_url)
      refresh()
      toast('تصویر به‌روز شد.')
    },
  })
  const removeImage = useMutation({
    mutationFn: () => catalogApi.deleteImage(product!.id),
    onSuccess: () => {
      setImage(null)
      refresh()
    },
  })
  const invalid = !f.name.trim() || !price || !f.category_id

  return (
    <Dialog
      open
      onClose={onClose}
      title={product ? `ویرایش ${product.name}` : 'محصول جدید'}
      className="sm:max-w-lg"
      footer={
        <>
          <Button className="flex-1" disabled={invalid || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? 'در حال ذخیره…' : 'ذخیره'}
          </Button>
          <Button variant="outline" className="flex-1" onClick={onClose}>
            انصراف
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="نام">{(p) => <input {...p} className={inputClass} maxLength={80} value={f.name} onChange={(e) => set('name', e.target.value)} />}</Field>
        <Field label="دسته‌بندی">
          {(p) => (
            <Select {...p} value={f.category_id} onChange={(e) => set('category_id', e.target.value)}>
              {categories.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                  {!c.is_active ? ' (غیرفعال)' : ''}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="توضیحات (اختیاری)">
          {(p) => <TextArea {...p} maxLength={300} value={f.description} onChange={(e) => set('description', e.target.value)} />}
        </Field>
        <Field label="قیمت فروش" hint={product ? 'تغییر قیمت در گزارش تغییرات ثبت می‌شود.' : undefined}>
          {(p) => <MoneyInput {...p} value={f.price} onChange={(v) => set('price', v)} />}
        </Field>
        <Field label="نوع موجودی" hint="«با کنترل موجودی» برای اقلام شمارشی مثل کیک؛ نوشیدنی‌های آماده‌سازی‌شده معمولاً بدون کنترل موجودی هستند.">
          {(p) => (
            <Select {...p} value={f.inventory_mode} onChange={(e) => set('inventory_mode', e.target.value as InventoryMode)}>
              <option value="UNTRACKED">{inventoryModeLabel.UNTRACKED}</option>
              <option value="TRACKED">{inventoryModeLabel.TRACKED}</option>
            </Select>
          )}
        </Field>
        {tracked ? (
          <Field label="حد هشدار موجودی (اختیاری)" hint="وقتی موجودی قابل فروش به این عدد برسد، هشدار نمایش داده می‌شود.">
            {(p) => <NumberInput {...p} value={f.threshold} onChange={(v) => set('threshold', v)} />}
          </Field>
        ) : (
          <Field label="هزینهٔ تخمینی هر واحد (اختیاری)" hint="فقط برای «حاشیهٔ سود تخمینی» در گزارش‌ها؛ وارد حساب سود نمی‌شود.">
            {(p) => <MoneyInput {...p} value={f.estimate} onChange={(v) => set('estimate', v)} />}
          </Field>
        )}
        <Field label="ترتیب نمایش">{(p) => <NumberInput {...p} value={f.sort_order} onChange={(v) => set('sort_order', v)} />}</Field>
        {product && (
          <div className="flex items-center justify-between rounded-lg border px-3">
            <span className="font-medium">نمایش در منو (فعال)</span>
            <Switch label="محصول فعال است" checked={f.is_active} onChange={(v) => set('is_active', v)} />
          </div>
        )}
        {product && (
          <fieldset className="space-y-2 rounded-lg border p-3">
            <legend className="px-1 text-sm font-medium">تصویر</legend>
            {image && <img src={image} alt={product.name} className="h-40 w-full rounded-lg object-cover" />}
            <div className="flex flex-wrap gap-2">
              <label className="inline-flex h-11 cursor-pointer items-center rounded-md border bg-surface px-4 font-medium hover:bg-muted has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50">
                {upload.isPending ? 'در حال بارگذاری…' : image ? 'تغییر تصویر' : 'افزودن تصویر'}
                <input
                  type="file"
                  accept={IMAGE_TYPES.join(',')}
                  className="sr-only"
                  disabled={upload.isPending}
                  onChange={(e) => {
                    const file = e.target.files?.[0]
                    const problem = file ? imageFileProblem(file) : null
                    setFileProblem(problem)
                    if (file && !problem) upload.mutate(file)
                    e.target.value = ''
                  }}
                />
              </label>
              {image && (
                <Button variant="outline" disabled={removeImage.isPending} onClick={() => removeImage.mutate()}>
                  حذف تصویر
                </Button>
              )}
            </div>
            <p className="text-xs text-muted-foreground">JPEG، PNG یا WebP، حداکثر ۳ مگابایت.</p>
            {fileProblem ? (
              <p role="alert" className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
                {fileProblem}
              </p>
            ) : (
              <FormError error={upload.error ?? removeImage.error} />
            )}
          </fieldset>
        )}
        <FormError error={save.error} />
      </div>
    </Dialog>
  )
}

function CategoryDialog({ category, onClose }: { category: Category | null; onClose: () => void }) {
  const [name, setName] = useState(category?.name ?? '')
  const [sort, setSort] = useState(String(category?.sort_order ?? 0))
  const [active, setActive] = useState(category?.is_active ?? true)
  const refresh = useRefreshCatalog()
  const save = useMutation({
    mutationFn: () =>
      category
        ? catalogApi.updateCategory(category.id, { name: name.trim(), sort_order: Number(sort || 0), is_active: active })
        : catalogApi.createCategory({ name: name.trim(), sort_order: Number(sort || 0) }),
    onSuccess: () => {
      refresh()
      onClose()
    },
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={category ? `ویرایش دستهٔ ${category.name}` : 'دستهٔ جدید'}
      footer={
        <>
          <Button className="flex-1" disabled={!name.trim() || save.isPending} onClick={() => save.mutate()}>
            ذخیره
          </Button>
          <Button variant="outline" className="flex-1" onClick={onClose}>
            انصراف
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="نام دسته">{(p) => <input {...p} className={inputClass} maxLength={60} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <Field label="ترتیب نمایش">{(p) => <NumberInput {...p} value={sort} onChange={setSort} />}</Field>
        {category && (
          <div className="flex items-center justify-between rounded-lg border px-3">
            <span className="font-medium">نمایش در منو</span>
            <Switch label="دسته فعال است" checked={active} onChange={setActive} />
          </div>
        )}
        <FormError error={save.error} />
      </div>
    </Dialog>
  )
}

export function CatalogPage() {
  const { can } = useAuth()
  const manage = can('catalog.manage')
  const { categories, products } = useCatalog()
  const [editing, setEditing] = useState<Product | 'new' | null>(null)
  const [editingCat, setEditingCat] = useState<Category | 'new' | null>(null)

  if (categories.isPending || products.isPending) return <LoadingState />
  if (categories.isError || products.isError) {
    return (
      <ErrorState
        error={categories.error ?? products.error}
        onRetry={() => {
          void categories.refetch()
          void products.refetch()
        }}
      />
    )
  }
  const cats = [...categories.data.items].sort((a, b) => a.sort_order - b.sort_order || a.id - b.id)
  const list = [...products.data.items].sort((a, b) => a.sort_order - b.sort_order || a.id - b.id)

  return (
    <div className="space-y-4">
      <PageHeader
        title="منو و محصولات"
        description={manage ? 'قیمت، دسته‌بندی، تصویر و موجود بودن محصولات' : 'برای اقلامی که تمام شده‌اند «تمام شد» بزنید.'}
        actions={
          manage && (
            <>
              <Button onClick={() => setEditing('new')} disabled={cats.length === 0}>
                محصول جدید
              </Button>
              <Button variant="outline" onClick={() => setEditingCat('new')}>
                دستهٔ جدید
              </Button>
            </>
          )
        }
      />
      {cats.length === 0 ? (
        <EmptyState title="هنوز دسته‌ای ساخته نشده" description={manage ? 'ابتدا یک دسته بسازید.' : undefined} />
      ) : (
        cats.map((c) => {
          const items = list.filter((p) => p.category_id === c.id)
          return (
            <Card key={c.id} aria-labelledby={`cat-${c.id}`}>
              <div className="flex items-center justify-between gap-2">
                <h2 id={`cat-${c.id}`} className="text-lg font-extrabold">
                  {c.name} <span className="text-sm font-normal text-muted-foreground">({faDigits(items.length)})</span>
                </h2>
                <div className="flex items-center gap-2">
                  {!c.is_active && <Badge tone="danger">غیرفعال</Badge>}
                  {manage && (
                    <Button variant="ghost" onClick={() => setEditingCat(c)} aria-label={`ویرایش دستهٔ ${c.name}`}>
                      ویرایش دسته
                    </Button>
                  )}
                </div>
              </div>
              {items.length === 0 ? (
                <p className="py-3 text-sm text-muted-foreground">محصولی در این دسته نیست.</p>
              ) : (
                <ul className="divide-y">
                  {items.map((p) => (
                    <ProductRow key={p.id} product={p} onEdit={manage ? () => setEditing(p) : undefined} />
                  ))}
                </ul>
              )}
            </Card>
          )
        })
      )}
      {editing && <ProductDialog product={editing === 'new' ? null : editing} categories={cats} onClose={() => setEditing(null)} />}
      {editingCat && <CategoryDialog category={editingCat === 'new' ? null : editingCat} onClose={() => setEditingCat(null)} />}
    </div>
  )
}

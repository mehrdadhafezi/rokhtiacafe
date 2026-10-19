import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { isApiError } from '@/lib/api'

import { pushApi } from '../api'
import { formError } from '../format'
import { useToast } from '../hooks'
import { Badge, Card } from '../ui'
import { browserEnv, currentSubscription, detectSupport, staffRegistration, subscriptionBody, urlBase64ToUint8Array } from './push'

type DeviceState = 'checking' | 'on' | 'off'

/**
 * Low-stock notifications for this device (ADMIN). Permission is requested only when the
 * «فعال‌سازی اعلان‌ها» button is pressed (AC-42).
 */
export function PushCard() {
  const toast = useToast()
  const support = detectSupport(browserEnv())
  const key = useQuery({
    queryKey: ['admin', 'push', 'key'],
    queryFn: ({ signal }) => pushApi.publicKey(signal),
    enabled: support === 'supported',
    staleTime: Infinity,
    retry: false,
  })
  const [device, setDevice] = useState<DeviceState>('checking')
  const [permission, setPermission] = useState<NotificationPermission>(() =>
    'Notification' in window ? Notification.permission : 'default',
  )
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (support !== 'supported') return
    let live = true
    currentSubscription()
      .then((s) => live && setDevice(s && Notification.permission === 'granted' ? 'on' : 'off'))
      .catch(() => live && setDevice('off'))
    return () => {
      live = false
    }
  }, [support])

  const enable = useMutation({
    mutationFn: async () => {
      setError(null)
      // Must be the first await: browsers only allow the prompt during the button press.
      const result = await Notification.requestPermission()
      setPermission(result)
      if (result !== 'granted') throw new Error('permission')
      const reg = await staffRegistration()
      if (!reg) throw new Error('no-worker')
      const sub =
        (await reg.pushManager.getSubscription()) ??
        (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToUint8Array(key.data!.key) }))
      try {
        await pushApi.subscribe(subscriptionBody(sub.toJSON()))
      } catch (err) {
        await sub.unsubscribe().catch(() => {}) // keep the device and the server in step
        throw err
      }
    },
    onSuccess: () => {
      setDevice('on')
      toast('اعلان‌ها روی این دستگاه فعال شد.')
    },
    onError: (err) => {
      if (err instanceof Error && err.message === 'permission') return // explained by the permission state
      setError(
        err instanceof Error && err.message === 'no-worker'
          ? 'سرویس پس‌زمینهٔ پنل هنوز آماده نیست. صفحه را یک بار دوباره بارگذاری کنید و دوباره تلاش کنید.'
          : isApiError(err)
            ? formError(err)
            : 'فعال‌سازی اعلان ممکن نشد. اتصال اینترنت را بررسی و دوباره تلاش کنید.',
      )
    },
  })

  const disable = useMutation({
    mutationFn: async () => {
      setError(null)
      const sub = await currentSubscription()
      if (sub) {
        await pushApi.unsubscribe(sub.endpoint)
        await sub.unsubscribe()
      }
    },
    onSuccess: () => {
      setDevice('off')
      toast('اعلان‌ها روی این دستگاه غیرفعال شد.')
    },
    onError: (err) => setError(formError(err)),
  })

  const test = useMutation({
    mutationFn: pushApi.test,
    onSuccess: () => toast('اعلان آزمایشی ارسال شد؛ معمولاً تا چند ثانیه دیگر می‌رسد.'),
    onError: (err) => setError(isApiError(err, 'CONFLICT') ? 'این دستگاه هنوز ثبت نشده است. یک بار اعلان‌ها را غیرفعال و دوباره فعال کنید.' : formError(err)),
  })

  let status: { tone: 'success' | 'neutral' | 'warning' | 'danger'; label: string; text: string }
  if (support === 'insecure') {
    status = { tone: 'warning', label: 'در دسترس نیست', text: 'اعلان فقط روی اتصال امن (https) کار می‌کند.' }
  } else if (support === 'ios-needs-install') {
    status = {
      tone: 'warning',
      label: 'نیاز به نصب',
      text: 'در آیفون و آیپد، ابتدا از منوی اشتراک‌گذاری «افزودن به صفحهٔ اصلی» را بزنید و پنل را از آیکون نصب‌شده باز کنید (iOS 16.4 یا جدیدتر).',
    }
  } else if (support === 'unsupported') {
    status = { tone: 'neutral', label: 'پشتیبانی نمی‌شود', text: 'این مرورگر از اعلان‌های وب پشتیبانی نمی‌کند. از Chrome یا Firefox استفاده کنید.' }
  } else if (key.isError && isApiError(key.error, 'PUSH_NOT_CONFIGURED')) {
    status = { tone: 'neutral', label: 'پیکربندی نشده', text: 'اعلان‌ها هنوز روی سرور راه‌اندازی نشده‌اند.' }
  } else if (permission === 'denied') {
    status = {
      tone: 'danger',
      label: 'مسدود شده',
      text: 'اجازهٔ اعلان برای این سایت در تنظیمات مرورگر مسدود است. برای فعال‌سازی، آن را از تنظیمات سایت در مرورگر روشن کنید.',
    }
  } else if (device === 'on') {
    status = { tone: 'success', label: 'فعال', text: 'هشدار موجودی کم روی این دستگاه نمایش داده می‌شود.' }
  } else {
    status = { tone: 'neutral', label: 'غیرفعال', text: 'با فعال‌سازی، وقتی موجودی کالایی به حد هشدار برسد روی این دستگاه اعلان می‌گیرید.' }
  }
  const canEnable = support === 'supported' && key.isSuccess && permission !== 'denied' && device === 'off'
  const busy = enable.isPending || disable.isPending || test.isPending

  return (
    <Card aria-labelledby="push-title">
      <div className="flex items-start justify-between gap-3">
        <h2 id="push-title" className="font-bold">
          اعلان موجودی کم
        </h2>
        <Badge tone={status.tone}>{status.label}</Badge>
      </div>
      <p className="mt-2 text-sm text-muted-foreground">{status.text}</p>
      {error && (
        <p role="alert" className="mt-3 rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
          {error}
        </p>
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        {canEnable && (
          <Button disabled={busy} onClick={() => enable.mutate()}>
            {enable.isPending ? 'در حال فعال‌سازی…' : 'فعال‌سازی اعلان‌ها'}
          </Button>
        )}
        {device === 'on' && support === 'supported' && permission === 'granted' && (
          <>
            <Button variant="outline" disabled={busy} onClick={() => test.mutate()}>
              {test.isPending ? 'در حال ارسال…' : 'ارسال اعلان آزمایشی'}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => disable.mutate()}>
              غیرفعال‌سازی
            </Button>
          </>
        )}
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        ارسال اعلان به سرویس مرورگر وابسته است و تضمینی نیست؛ فهرست موجودی کم در داشبورد همیشه به‌روز است.
      </p>
    </Card>
  )
}

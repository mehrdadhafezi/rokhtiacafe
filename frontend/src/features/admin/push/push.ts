/**
 * Web Push on the staff device (docs/WORKFLOWS.md §4.3). Permission is requested only from the
 * «فعال‌سازی اعلان‌ها» button; the subscription is sent to the server, which delivers low-stock
 * alerts best-effort through the browser's push service.
 */

export type PushSupport = 'supported' | 'insecure' | 'unsupported' | 'ios-needs-install'

export interface BrowserEnv {
  secure: boolean
  serviceWorker: boolean
  pushManager: boolean
  notification: boolean
  ios: boolean
  standalone: boolean
}

export function detectSupport(env: BrowserEnv): PushSupport {
  if (!env.secure) return 'insecure'
  // iOS/iPadOS 16.4+ exposes Web Push only to a home-screen (installed) web app.
  if (env.ios && !env.standalone) return 'ios-needs-install'
  if (!env.serviceWorker || !env.pushManager || !env.notification) return 'unsupported'
  return 'supported'
}

export function browserEnv(): BrowserEnv {
  const nav = navigator as Navigator & { standalone?: boolean }
  const ua = nav.userAgent
  const ios = /iPad|iPhone|iPod/.test(ua) || (ua.includes('Macintosh') && nav.maxTouchPoints > 1)
  return {
    secure: window.isSecureContext,
    serviceWorker: 'serviceWorker' in nav,
    pushManager: 'PushManager' in window,
    notification: 'Notification' in window,
    ios,
    standalone: window.matchMedia?.('(display-mode: standalone)').matches || nav.standalone === true,
  }
}

/** VAPID public key (base64url) → bytes for `applicationServerKey`. */
export function urlBase64ToUint8Array(base64url: string): Uint8Array<ArrayBuffer> {
  const padded = base64url.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (base64url.length % 4)) % 4)
  const raw = atob(padded)
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

/** The body the API accepts: endpoint and keys only (unknown fields are rejected with 422). */
export function subscriptionBody(json: PushSubscriptionJSON): { endpoint: string; keys: { p256dh: string; auth: string } } {
  if (!json.endpoint || !json.keys?.p256dh || !json.keys.auth) throw new Error('incomplete push subscription')
  return { endpoint: json.endpoint, keys: { p256dh: json.keys.p256dh, auth: json.keys.auth } }
}

/** The staff service worker registration, waiting at most `timeoutMs` for it to activate. */
export async function staffRegistration(timeoutMs = 10_000): Promise<ServiceWorkerRegistration | null> {
  const existing = await navigator.serviceWorker.getRegistration('/admin/')
  if (!existing) return null
  if (existing.active) return existing
  return Promise.race([
    navigator.serviceWorker.ready,
    new Promise<null>((resolve) => setTimeout(() => resolve(null), timeoutMs)),
  ])
}

export async function currentSubscription(): Promise<PushSubscription | null> {
  const reg = await navigator.serviceWorker.getRegistration('/admin/')
  return (await reg?.pushManager.getSubscription()) ?? null
}

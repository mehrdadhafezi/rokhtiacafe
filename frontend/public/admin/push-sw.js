/*
 * Web Push handlers for the staff app, imported by the generated /admin/sw.js (scope /admin/).
 * Customer pages have no service worker, so they never receive staff notifications.
 * Payload (from the server outbox): { type, title, body, url, product_id? }.
 */
const STAFF_SCOPE = new URL('/admin/', self.location.origin).href

/** Only same-origin URLs inside the staff app may be opened from a notification. */
function staffUrl(raw) {
  try {
    const u = new URL(raw, self.location.origin)
    if (u.origin === self.location.origin && u.href.startsWith(STAFF_SCOPE)) return u.href
  } catch {
    /* fall through */
  }
  return STAFF_SCOPE
}

self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = {}
  }
  const title = typeof data.title === 'string' && data.title ? data.title : 'رخ تیا'
  const body = typeof data.body === 'string' ? data.body : ''
  // One visible notification per product: a newer low-stock alert replaces the older one.
  const tag = data.type === 'LOW_STOCK' && data.product_id ? `low-stock-${data.product_id}` : 'rokh-staff'
  event.waitUntil(
    self.registration.showNotification(title, {
      body,
      dir: 'rtl',
      lang: 'fa',
      icon: '/admin/icons/icon-192.png',
      badge: '/admin/icons/icon-192.png',
      tag,
      renotify: true,
      data: { url: staffUrl(data.url) },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = staffUrl(event.notification.data && event.notification.data.url)
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      const staff = windows.find((c) => c.url.startsWith(STAFF_SCOPE))
      if (staff) {
        // Navigate first: focusing can be refused, and must not stop the page from opening.
        const moved = 'navigate' in staff ? await staff.navigate(url).catch(() => null) : null
        await (moved || staff).focus().catch(() => {})
        if (moved) return
      }
      await self.clients.openWindow(url)
    })(),
  )
})

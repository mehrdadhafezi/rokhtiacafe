/**
 * Registers the staff service worker (built to /admin/sw.js by vite-plugin-pwa).
 * Production builds only: in dev the worker would cache Vite's modules.
 * Scope is /admin/ so it never controls customer pages (SECURITY §11).
 */
export function registerServiceWorker() {
  if (!import.meta.env.PROD || !('serviceWorker' in navigator)) return
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/admin/sw.js', { scope: '/admin/' }).catch((err: unknown) => {
      console.error('service worker registration failed', err)
    })
  })
}

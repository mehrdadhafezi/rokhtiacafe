// Regression R-F12-1 (SECURITY §11): only the staff app is a PWA.
// Run after `npm run build`; exits non-zero on any violation.
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const dist = resolve(fileURLToPath(new URL('..', import.meta.url)), 'dist')
const read = (p) => readFileSync(resolve(dist, p), 'utf8')
const failures = []
const check = (ok, msg) => {
  if (!ok) failures.push(msg)
}

const manifestLink = /<link[^>]*rel=["']?manifest/i

// Customer page: no manifest link, no service worker registration anywhere in its bundle.
const customerHtml = read('index.html')
check(!manifestLink.test(customerHtml), 'dist/index.html must not link a web app manifest')
check(!existsSync(resolve(dist, 'sw.js')), 'there must be no root-scoped dist/sw.js')
check(!existsSync(resolve(dist, 'registerSW.js')), 'there must be no injected registerSW.js')

const viteManifest = JSON.parse(read('.vite/manifest.json'))
const reachable = (entry) => {
  const files = new Set()
  const visit = (key) => {
    const chunk = viteManifest[key]
    if (!chunk || files.has(chunk.file)) return
    files.add(chunk.file)
    chunk.imports?.forEach(visit)
    chunk.dynamicImports?.forEach(visit)
  }
  visit(entry)
  return files
}
const customerJs = reachable('index.html')
const adminJs = reachable('admin/index.html')
check(customerJs.size > 0 && adminJs.size > 0, 'both entries must be in the Vite manifest')
for (const file of customerJs) {
  check(!read(file).includes('serviceWorker'), `customer chunk ${file} must not reference serviceWorker`)
}
check(!customerJs.has(viteManifest['admin/index.html'].file), 'customer bundle must not load the admin entry chunk')

// Staff page: manifest linked, scoped to /admin/, worker at /admin/sw.js.
const adminHtml = read('admin/index.html')
check(/<link[^>]*rel=["']?manifest["']?[^>]*href=["']?\/admin\/manifest\.webmanifest/i.test(adminHtml), 'dist/admin/index.html must link /admin/manifest.webmanifest')
const webManifest = JSON.parse(read('admin/manifest.webmanifest'))
check(webManifest.scope === '/admin/', 'manifest scope must be /admin/')
check(webManifest.start_url === '/admin/', 'manifest start_url must be /admin/')
check(webManifest.name === 'رخ تیا — پنل کارکنان', 'manifest name must be «رخ تیا — پنل کارکنان»')
check(webManifest.dir === 'rtl' && webManifest.lang === 'fa', 'manifest must be dir=rtl lang=fa')
check(existsSync(resolve(dist, 'admin/sw.js')), 'dist/admin/sw.js must exist')

// The worker precaches the staff shell only.
const sw = read('admin/sw.js')
check(sw.includes('"/admin/index.html"'), 'service worker must precache /admin/index.html')
check(!/"\/index\.html"/.test(sw), 'service worker must not precache the customer index.html')
const customerOnly = [...customerJs].filter((f) => !adminJs.has(f))
for (const file of customerOnly) {
  check(!sw.includes(`"/${file}"`), `service worker must not precache customer chunk ${file}`)
}

if (failures.length) {
  console.error('PWA separation check failed:\n- ' + failures.join('\n- '))
  process.exit(1)
}
console.log(`PWA separation check passed (customer chunks: ${customerJs.size}, admin chunks: ${adminJs.size}).`)

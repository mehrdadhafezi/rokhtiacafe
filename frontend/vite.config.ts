/// <reference types="vitest/config" />
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Connect, type Plugin } from 'vite'
import { VitePWA } from 'vite-plugin-pwa'

const root = fileURLToPath(new URL('.', import.meta.url))
const outDir = resolve(root, 'dist')
const backend = 'http://127.0.0.1:8080'

// Deep links of the staff app (/admin/orders, ...) must get admin/index.html in dev and
// preview; Vite's default SPA fallback would serve the customer index.html instead.
function adminFallback(): Plugin {
  const rewrite: Connect.NextHandleFunction = (req, _res, next) => {
    const path = req.url?.split('?')[0] ?? ''
    if ((path === '/admin' || path.startsWith('/admin/')) && !/\.[a-z0-9]+$/i.test(path)) {
      req.url = '/admin/index.html'
    }
    next()
  }
  return {
    name: 'rokh-admin-fallback',
    configureServer: (server) => void server.middlewares.use(rewrite),
    configurePreviewServer: (server) => void server.middlewares.use(rewrite),
  }
}

type ViteManifest = Record<string, { file: string; css?: string[]; assets?: string[]; imports?: string[] }>

// Files reachable from the staff entry (JS chunks, CSS, fonts), read from Vite's build manifest.
// The service worker precaches only these, never the customer app (SECURITY §11).
function adminShellFiles(): Set<string> {
  const manifest = JSON.parse(readFileSync(resolve(outDir, '.vite/manifest.json'), 'utf8')) as ViteManifest
  const files = new Set<string>(['admin/index.html'])
  const visit = (key: string) => {
    const chunk = manifest[key]
    if (!chunk || files.has(chunk.file)) return
    files.add(chunk.file)
    chunk.css?.forEach((f) => files.add(f))
    chunk.assets?.forEach((f) => files.add(f))
    chunk.imports?.forEach(visit)
  }
  visit('admin/index.html')
  return files
}

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    adminFallback(),
    // Staff PWA only. The manifest is a static file linked solely from admin/index.html
    // (manifest: false stops the plugin from injecting it into every page) and the app
    // registers the worker itself (injectRegister: false).
    VitePWA({
      manifest: false,
      injectRegister: false,
      filename: 'admin/sw.js',
      scope: '/admin/',
      includeManifestIcons: false,
      workbox: {
        globPatterns: ['**/*.{html,js,css,woff2,svg,png,webmanifest}'],
        manifestTransforms: [
          (entries) => {
            const shell = adminShellFiles()
            // URLs are already absolute here (modifyURLPrefix runs first).
            const keep = (url: string) => shell.has(url.slice(1)) || url.startsWith('/admin/')
            return { manifest: entries.filter((e) => keep(e.url)), warnings: [] }
          },
        ],
        // Precache URLs are resolved against the worker location (/admin/); make them absolute.
        modifyURLPrefix: { '': '/' },
        navigateFallback: '/admin/index.html',
        navigateFallbackAllowlist: [/^\/admin(\/|$)/],
        // API calls are network-only: no stale prices or order states.
        runtimeCaching: [{ urlPattern: ({ url }) => url.pathname.startsWith('/api/'), handler: 'NetworkOnly' }],
        cleanupOutdatedCaches: true,
      },
    }),
  ],
  resolve: {
    alias: { '@': resolve(root, 'src') },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': backend,
      '/healthz': backend,
      '/readyz': backend,
      '/media': backend,
    },
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
    proxy: {
      '/api': backend,
      '/healthz': backend,
      '/readyz': backend,
      '/media': backend,
    },
  },
  build: {
    outDir,
    emptyOutDir: true,
    manifest: true,
    rollupOptions: {
      // Neutral names for shared chunks (Rollup would otherwise name them after a random module).
      output: {
        chunkFileNames: 'assets/chunk-[hash].js',
        assetFileNames: (asset) =>
          asset.names.some((n) => n.endsWith('.css')) ? 'assets/style-[hash][extname]' : 'assets/[name]-[hash][extname]',
      },
      input: {
        customer: resolve(root, 'index.html'),
        admin: resolve(root, 'admin/index.html'),
      },
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})

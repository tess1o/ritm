// Minimal service worker (FE-9): caches the app shell and an offline page; API calls always
// go to the network (no offline data mode).
const CACHE = 'ritm-shell-v1'
const SHELL = ['/', '/offline.html', '/manifest.webmanifest', '/icons/icon.svg', '/icons/icon-192.png']

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()))
})

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url)
  if (e.request.method !== 'GET' || url.origin !== location.origin) return
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/mcp') || url.pathname === '/healthz') return
  if (e.request.mode === 'navigate') {
    // network first so new deploys show up; offline page when the server is unreachable
    e.respondWith(fetch(e.request).catch(() => caches.match('/offline.html')))
    return
  }
  if (url.pathname.startsWith('/assets/')) {
    // hashed, immutable assets: cache first
    e.respondWith(
      caches.match(e.request).then(
        (hit) =>
          hit ||
          fetch(e.request).then((res) => {
            if (res.ok) {
              const copy = res.clone()
              caches.open(CACHE).then((c) => c.put(e.request, copy))
            }
            return res
          }),
      ),
    )
  }
})

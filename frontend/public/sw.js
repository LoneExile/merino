// Hand-rolled service worker for the Herdr Tunnel PWA.
//
// Scope: cache the static app shell so the dashboard opens instantly (and
// offline) from a home-screen icon. Deliberately NOT caching /api/*: a
// cached agent list or terminal snapshot presented as live state would be
// actively misleading for a tool whose entire job is showing what an agent
// is doing *right now*.
//
// Strategy:
//   - Navigations (HTML)              -> network-first, falling back to the
//     cached shell when offline. A stale shell is fine offline; it just
//     re-fetches the (also cache-first) hashed JS/CSS once back online.
//   - Same-origin hashed build assets -> cache-first. Vite content-hashes
//     everything under /assets/, so a cache hit is always the exact byte the
//     current index.html expects — there's no staleness to worry about.
//   - Everything else (notably /api/*, and cross-origin requests) is left
//     alone and goes straight to the network.
//
// Bump CACHE_VERSION whenever SHELL_URLS changes, so `activate` evicts the
// old cache instead of accumulating dead entries forever.
const CACHE_VERSION = "v2";
const CACHE_NAME = `herdr-shell-${CACHE_VERSION}`;

// The minimal shell needed to boot the app while offline. Hashed build
// output (JS/CSS/fonts under /assets/) is deliberately NOT listed here —
// Vite renames those on every build, so a static list would require
// generating this file at build time, which is exactly the
// vite-plugin-pwa-shaped complexity this hand-rolled worker avoids. They're
// picked up lazily by the cache-first fetch handler below on first load
// instead.
const SHELL_URLS = [
  "/",
  "/index.html",
  "/manifest.webmanifest",
  "/icon-192.png",
  "/icon-512.png",
  "/icon-512-maskable.png",
  "/apple-touch-icon.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) =>
      // cache.addAll() fails the whole install if a single URL 404s. Add
      // individually and swallow per-URL failures instead, so one missing
      // icon can never block index.html — the one entry that actually
      // matters for offline boot — from being cached.
      Promise.all(SHELL_URLS.map((url) => cache.add(url).catch(() => {}))),
    ),
  );
  // Activate this worker as soon as it finishes installing rather than
  // waiting for every open tab to close first.
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== CACHE_NAME).map((key) => caches.delete(key))))
      .then(() => self.clients.claim()),
  );
});

// True for Vite's content-hashed build output: /assets/<name>-<hash>.<ext>.
function isHashedAsset(url) {
  return url.pathname.startsWith("/assets/");
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  // Never intercept the API. See the file-level comment: a cached response
  // here would be shown as live state and that is never acceptable for this
  // app.
  if (url.pathname.startsWith("/api/")) return;

  if (request.mode === "navigate") {
    event.respondWith(networkFirst(request));
    return;
  }

  if (isHashedAsset(url)) {
    event.respondWith(cacheFirst(request));
  }
});

async function networkFirst(request) {
  try {
    const response = await fetch(request);
    // Only cache a clean 200 for the exact URL requested. A redirected
    // response (e.g. an expired session bouncing "/" to the login page)
    // must never be cached under the "/" key, or a later offline visit
    // would show the login page as the app shell even after signing back
    // in.
    if (response.ok && !response.redirected) {
      const cache = await caches.open(CACHE_NAME);
      cache.put(request, response.clone());
    }
    return response;
  } catch {
    const cached = await caches.match(request);
    return cached || caches.match("/index.html");
  }
}

async function cacheFirst(request) {
  const cached = await caches.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  if (response.ok) {
    const cache = await caches.open(CACHE_NAME);
    cache.put(request, response.clone());
  }
  return response;
}

// --- Push notifications --------------------------------------------------
// Intentionally empty. Left as explicit listeners (rather than omitted
// entirely) so it's obvious this service worker is push-aware and exactly
// where that logic belongs. Filled in separately — see the push
// notification work.

self.addEventListener("push", (event) => {
  // A missing or malformed payload must still show SOMETHING — a
  // notification that silently never appears looks exactly like a bug, not
  // like "nothing happened".
  let payload = { title: "Herdr Tunnel", body: "An agent needs you." };
  try {
    if (event.data) payload = { ...payload, ...event.data.json() };
  } catch {
    // Not JSON; keep the fallback above.
  }
  const paneId = payload.data?.paneId;

  event.waitUntil(
    self.registration.showNotification(payload.title, {
      body: payload.body,
      icon: "/icon-192.png",
      badge: "/icon-192.png",
      // Keyed to the pane: a second "needs you" push for the SAME pane
      // replaces the notification already showing rather than stacking a
      // pile of stale ones for a prompt that was answered long ago.
      tag: paneId,
      data: { paneId },
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  const paneId = event.notification.data?.paneId;
  event.notification.close();

  const targetUrl = paneId ? `/?pane=${encodeURIComponent(paneId)}` : "/";

  event.waitUntil(
    (async () => {
      const windows = await clients.matchAll({ type: "window", includeUncontrolled: true });
      const existing = windows[0];
      if (existing) {
        // Prefer bringing the dashboard the user already has open to the
        // front over spawning a second tab, and steer it at the pane —
        // navigate() changes an existing window's URL without the
        // OS-window churn a second openWindow would cause.
        if ("navigate" in existing) {
          try {
            await existing.navigate(targetUrl);
          } catch {
            // Cross-origin or otherwise refused by the browser; focusing
            // the tab as-is is still strictly better than doing nothing.
          }
        }
        return existing.focus();
      }
      return clients.openWindow(targetUrl);
    })(),
  );
});

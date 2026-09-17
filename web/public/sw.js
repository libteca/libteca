// libteca service worker — allowlist-cached static assets and the app shell.
// Everything else (API, media, protocol faces, anything authenticated, any
// request with a query string or Range header) always goes to the network:
// a denylist used to let /rest/*, /opds/* and other authenticated protocol
// responses into the cache, where they could be replayed after logout.
// Activation deletes only caches libteca owns (libteca-static-* plus the
// known legacy names); CacheStorage is origin-wide, so an origin-wide sweep
// would delete unrelated applications' offline data.
//
// What works offline:
//   - the shell route / (navigations)
//   - every /assets/ chunk fetched once while online, which includes the
//     reader's lazy chunks (epubjs, jszip, the shared commonjs chunk)
//   - the static icon/manifest set below
//   - NOT offline: book files, streams, covers, all protocol faces
const PREFIX = "libteca-static-";
const CACHE = PREFIX + "v5";
const LEGACY = ["libteca-libteca-v1", "libteca-libteca-v2", "libteca-libteca-v3", "libteca-libteca-v4"];
const STATIC = new Set([
  "/icon.svg",
  "/icon-192.png",
  "/icon-512.png",
  "/icon-maskable-512.png",
  "/apple-touch-icon.png",
  "/manifest.webmanifest",
]);

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.add("/"))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys();
      await Promise.all(
        names
          .filter((n) => (n.startsWith(PREFIX) && n !== CACHE) || LEGACY.includes(n))
          .map((n) => caches.delete(n))
      );
      await self.clients.claim();
    })()
  );
});

async function cachedStatic(request, shell) {
  let cache = null;
  try {
    cache = await caches.open(CACHE);
  } catch {
    /* CacheStorage unavailable: the network path still works */
  }
  if (!shell && cache) {
    try {
      const hit = await cache.match(request);
      if (hit) return hit;
    } catch {
      /* match failures must not shadow the network */
    }
  }
  let response;
  try {
    response = await fetch(request);
  } catch (networkError) {
    if (shell && cache) {
      try {
        const hit = await cache.match("/");
        if (hit) return hit;
      } catch {
        /* surface the real network failure */
      }
    }
    throw networkError;
  }
  const noStore = /\bno-store\b/i.test(response.headers.get("Cache-Control") || "");
  const isHTML = /text\/html/i.test(response.headers.get("Content-Type") || "");
  if (cache && response.status === 200 && !noStore && (!shell || isHTML)) {
    try {
      await cache.put(shell ? "/" : request, response.clone());
    } catch {
      /* quota/storage failures must not discard a good network response */
    }
  }
  return response;
}

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin || url.search) return;
  if (request.headers.has("Authorization") || request.headers.has("Range")) return;
  const shell = url.pathname === "/";
  const asset = url.pathname.startsWith("/assets/") || STATIC.has(url.pathname);
  if (!shell && !asset) return;
  if (request.mode === "navigate" && !shell) return;
  event.respondWith(cachedStatic(request, shell));
});

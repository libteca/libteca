// libteca service worker — network-first for the app shell (navigations),
// never for media/API. Version-keyed; old caches are dropped on activate.
//
// What works offline:
//   - any navigation to / (the shell serves every hash route, #/read
//     included — hash changes are same-document, only / hits the network)
//   - every /assets/ chunk fetched once while online, which includes the
//     reader's lazy chunks (epubjs, jszip, the shared commonjs chunk) —
//     hashed names rule out install-time precaching from this verbatim
//     file, so reader code is cached on first reader use
//   - NOT offline: book files, streams, covers, everything under NEVER
const VERSION = "libteca-v4";
const CACHE = "libteca-" + VERSION;
const NEVER = [/\/api\//, /^\/s\//, /^\/stream\//, /^\/covers\//, /^\/subtitles\//, /^\/Videos\//, /^\/Audio\//];

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
      await Promise.all(names.filter((n) => n !== CACHE).map((n) => caches.delete(n)));
      await self.clients.claim();
    })()
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (NEVER.some((re) => re.test(url.pathname))) return;

  event.respondWith(
    (async () => {
      if (req.mode === "navigate" || url.pathname === "/") {
        try {
          const res = await fetch(req);
          if (res && res.ok) {
            const cache = await caches.open(CACHE);
            await cache.put(new Request("/"), res.clone());
          }
          return res;
        } catch (err) {
          const shell = await caches.match("/");
          if (shell) return shell;
          throw err;
        }
      }
      const cached = await caches.match(req);
      if (cached) return cached;
      try {
        const res = await fetch(req);
        if (res && res.ok) {
          const cache = await caches.open(CACHE);
          await cache.put(req, res.clone());
        }
        return res;
      } catch (err) {
        throw err;
      }
    })()
  );
});

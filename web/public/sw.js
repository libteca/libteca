// libteca service worker — cache-first for same-origin static assets,
// never for media/API. Version-keyed; old caches are dropped on activate.
const VERSION = "libteca-v1";
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
      const cached = await caches.match(req);
      if (cached) return cached;
      try {
        const res = await fetch(req);
        if (res && res.ok) {
          const key = req.mode === "navigate" ? new Request("/") : req;
          const cache = await caches.open(CACHE);
          await cache.put(key, res.clone());
        }
        return res;
      } catch (err) {
        if (req.mode === "navigate") {
          const shell = await caches.match("/");
          if (shell) return shell;
        }
        throw err;
      }
    })()
  );
});

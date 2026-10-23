const CACHE = "antares-public-0d3b04c48e052e3fb029e8e5f469e0e815b61535b16fbeba2837ece961612cfa";
const PUBLIC = ["/offline.html","/assets/Block-Dqc9rD5Z.js","/assets/ci-info-Bauk7qhZ.js","/assets/index-Bv14VpXm.js","/assets/index-CIkeuOtS.css","/assets/offline-52783b55d1fef27e.css","/assets/rolldown-runtime-BWaJ3C7D.js"];
const allowed = new Set(PUBLIC.map(path => new URL(path, self.location.origin).href));

self.addEventListener("install", event => {
  event.waitUntil((async () => {
    const cache = await caches.open(CACHE);
    for (const path of PUBLIC) {
      const response = await fetch(path, { credentials: "omit", cache: "reload" });
      if (!response.ok || response.redirected || /no-store/i.test(response.headers.get("Cache-Control") || "")) {
        throw new Error("Public asset unavailable");
      }
      await cache.put(path, response);
    }
  })());
});

self.addEventListener("activate", event => {
  event.waitUntil((async () => {
    for (const name of await caches.keys()) {
      if (name.startsWith("antares-public-") && name !== CACHE) await caches.delete(name);
    }
    await self.clients.claim();
  })());
});

self.addEventListener("fetch", event => {
  const { request } = event;
  const url = new URL(request.url);
  // Scope cannot exclude subpaths; API and foreign requests always bypass interception.
  if (request.method !== "GET" || url.origin !== self.location.origin || /^\/api(?:\/|$)/i.test(url.pathname)) return;
  if (request.mode === "navigate") {
    event.respondWith(fetch(request).catch(async () => {
      const cache = await caches.open(CACHE);
      return await cache.match("/offline.html") || Response.error();
    }));
  } else if (allowed.has(request.url)) {
    event.respondWith((async () => {
      const cache = await caches.open(CACHE);
      return await cache.match(request) || fetch(request);
    })());
  }
});

self.addEventListener("message", event => {
  if (event.data?.type === "ACTIVATE_UPDATE") event.waitUntil(self.skipWaiting());
  if (event.data?.type === "PURGE_USER_STATE") {
    event.waitUntil((async () => {
      for (const name of await caches.keys()) {
        if (!name.startsWith("antares-public-")) continue;
        if (name !== CACHE) continue;
        const cache = await caches.open(name);
        for (const request of await cache.keys()) {
          if (!allowed.has(request.url)) await cache.delete(request);
        }
      }
    })());
  }
});

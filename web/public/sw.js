const CACHE = "antares-public-__VERSION__";
const PUBLIC = __PRECACHE__;
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

// ---- Subscription rotation ---------------------------------------------------------------
// When the browser rotates its push subscription the old endpoint answers 404/410, the sender
// drops the device row, and alerts stop silently. This takes a fresh subscription with the
// same application server key and registers it with the session cookie. A failure is logged,
// never swallowed; the notifications panel also shows a browser with no registered device.

async function reregisterSubscription(event) {
  const reg = self.registration;
  let key = event.oldSubscription && event.oldSubscription.options && event.oldSubscription.options.applicationServerKey;
  if (!key) {
    // The browser did not hand over the old options: ask the server for the public key.
    const res = await fetch("/api/push/public-key", { credentials: "include", headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("public key unavailable: HTTP " + res.status);
    const body = await res.json();
    if (!body || typeof body.publicKey !== "string") throw new Error("public key unavailable: bad response");
    const pad = "=".repeat((4 - (body.publicKey.length % 4)) % 4);
    const bin = atob((body.publicKey + pad).replace(/-/g, "+").replace(/_/g, "/"));
    key = Uint8Array.from(bin, c => c.charCodeAt(0));
  }
  const sub = event.newSubscription || (await reg.pushManager.getSubscription()) || (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }));
  const json = sub.toJSON();
  if (!json.keys || !json.keys.p256dh || !json.keys.auth) throw new Error("subscription has no keys");
  const res = await fetch("/api/push/subscriptions", {
    method: "POST",
    credentials: "include",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint: sub.endpoint, keys: { p256dh: json.keys.p256dh, auth: json.keys.auth } }),
  });
  if (!res.ok) throw new Error("re-register refused: HTTP " + res.status);
}

self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(reregisterSubscription(event).catch((err) => {
    console.error("Antares: push re-register failed; the notifications panel will offer to turn it back on.", err);
  }));
});

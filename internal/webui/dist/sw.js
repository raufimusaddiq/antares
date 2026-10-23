const CACHE = "antares-public-89d20bf05747d65ef06136d668aeee63eb1f06e342e0413657dfadd8e45bb267";
const PUBLIC = ["/offline.html","/assets/Block-0vZ_GRp5.js","/assets/ci-info-Bauk7qhZ.js","/assets/index-Cbuhpik9.js","/assets/index-Civ-T-dP.css","/assets/offline-52783b55d1fef27e.css","/assets/rolldown-runtime-BWaJ3C7D.js"];
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

// ---- Web Push (UXP-002) ----------------------------------------------------------------
// A push carries {v, kind, chatId} and nothing else. The text shown is fixed here and chosen
// by `kind`; the payload is never rendered, so nothing in it can become text. chatId is used
// only after it matches an opaque-id pattern, to build the link. These handlers touch neither
// the cache nor fetch.

const CHAT_ID = /^[0-9a-f]{32}$/;
const TEXT = {
  completed: "Your reply is ready.",
  failed: "A run did not finish.",
};

function parse(data) {
  let p;
  try {
    p = data ? data.json() : null;
  } catch {
    return null;
  }
  if (!p || p.v !== 1 || !Object.prototype.hasOwnProperty.call(TEXT, p.kind)) return null;
  return { kind: p.kind, chatId: typeof p.chatId === "string" && CHAT_ID.test(p.chatId) ? p.chatId : "" };
}

self.addEventListener("push", (event) => {
  const p = parse(event.data);
  // An unreadable push still has to show something (browsers require it); it stays generic.
  const kind = p ? p.kind : "completed";
  const body = p ? TEXT[kind] : "Open Antares to see what happened.";
  event.waitUntil(
    self.registration.showNotification("Antares", {
      body,
      icon: "/apple-touch-icon.png",
      tag: p && p.chatId ? "antares-run-" + p.chatId : "antares-run", // one per chat: a second chat does not replace the first
      data: { chatId: p ? p.chatId : "" },
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const id = event.notification.data && event.notification.data.chatId;
  const url = typeof id === "string" && CHAT_ID.test(id) ? "/chat/" + id : "/";
  event.waitUntil(
    (async () => {
      const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      for (const w of wins) {
        if (new URL(w.url).origin === self.location.origin && "navigate" in w) {
          await w.focus();
          await w.navigate(url);
          return;
        }
      }
      await self.clients.openWindow(url);
    })(),
  );
});

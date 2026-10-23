// Antares service worker: Web Push only (UXP-002). It has no fetch handler and no
// cache, so it can never hold or serve a private response.
//
// A push carries {v, kind, chatId} and nothing else. The text shown is fixed here and
// chosen by `kind`; the payload is never rendered, so nothing in it can become text.
// chatId is used only after it matches an opaque-id pattern, to build the link.

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
      tag: "antares-run",
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

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { runInNewContext } from "node:vm";
import { describe, expect, it } from "vitest";
import { b64urlToBytes, bytesToB64url, chatIdFromPath, parseNotifications, unsupportedReason } from "./push";

const ID = "0123456789abcdef0123456789abcdef";

describe("support is reported, never assumed", () => {
  const ok = { serviceWorker: true, pushManager: true, notification: true, secure: true };
  it("is available when the browser has everything", () => expect(unsupportedReason(ok)).toBeNull());
  it("names the missing piece", () => {
    expect(unsupportedReason({ ...ok, secure: false })).toMatch(/secure/);
    for (const k of ["serviceWorker", "pushManager", "notification"] as const) {
      expect(unsupportedReason({ ...ok, [k]: false })).toMatch(/does not support/);
    }
  });
});

describe("deep link addresses", () => {
  it("accepts only an opaque chat id", () => {
    expect(chatIdFromPath(`/chat/${ID}`)).toBe(ID);
    expect(chatIdFromPath(`/chat/${ID}/`)).toBe(ID);
    for (const bad of ["/chat/", "/chat/abc", `/chat/${ID}x`, `/chat/${ID}/../admin`, `/chat/${ID.toUpperCase()}`, "/admin/users", `//chat/${ID}`, `/chat/${ID}?x=1`]) {
      expect(chatIdFromPath(bad), bad).toBeNull();
    }
  });
});

describe("encoding helpers", () => {
  it("round-trips base64url", () => {
    const b = new Uint8Array([251, 255, 0, 1, 2, 250]);
    expect([...b64urlToBytes(bytesToB64url(b.buffer))]).toEqual([...b]);
    expect(bytesToB64url(b.buffer)).not.toMatch(/[+/=]/);
  });
});

describe("notification settings are read strictly", () => {
  it("parses the server's answer", () => {
    expect(parseNotifications({ configured: true, mode: "all", devices: [{ id: "a", endpointHash: "h", createdAt: "x" }] })).toEqual({ configured: true, mode: "all", devices: [{ id: "a", endpointHash: "h" }] });
  });
  it("refuses anything else rather than guessing", () => {
    for (const bad of [null, {}, { configured: true, mode: "loud", devices: [] }, { configured: "yes", mode: "all", devices: [] }, { configured: true, mode: "all" }]) {
      expect(parseNotifications(bad)).toBeNull();
    }
  });
});

// Run the real worker file against a stub of the worker globals.
function worker() {
  const handlers: Record<string, (e: unknown) => void> = {};
  const shown: { title: string; opts: { body: string; data: { chatId: string } } }[] = [];
  const opened: string[] = [];
  const navigated: string[] = [];
  let winUrl: string | null = null;
  const self = {
    location: { origin: "https://chat.example.test" },
    addEventListener: (n: string, f: (e: unknown) => void) => (handlers[n] = f),
    registration: { showNotification: async (title: string, opts: never) => void shown.push({ title, opts }) },
    clients: {
      matchAll: async () => (winUrl ? [{ url: winUrl, focus: async () => {}, navigate: async (u: string) => void navigated.push(u) }] : []),
      openWindow: async (u: string) => void opened.push(u),
    },
  };
  runInNewContext(readFileSync(join(__dirname, "..", "public", "sw.js"), "utf8"), { self, URL, Object });
  const push = async (payload: unknown, raw = false) => {
    let waited: Promise<unknown> = Promise.resolve();
    handlers.push({ data: { json: () => (raw ? JSON.parse(payload as string) : payload) }, waitUntil: (p: Promise<unknown>) => (waited = p) });
    await waited;
  };
  const click = async (chatId: string) => {
    let waited: Promise<unknown> = Promise.resolve();
    handlers.notificationclick({ notification: { close() {}, data: { chatId } }, waitUntil: (p: Promise<unknown>) => (waited = p) });
    await waited;
  };
  return { push, click, shown, opened, navigated, setWindow: (u: string | null) => (winUrl = u) };
}

describe("the service worker shows fixed text and nothing from the payload", () => {
  it("shows generic text for each kind, whatever else the payload says", async () => {
    const w = worker();
    await w.push({ v: 1, kind: "completed", chatId: ID, title: "TITLE-SECRET", body: "BODY-SECRET", text: "TEXT-SECRET" });
    await w.push({ v: 1, kind: "failed", chatId: ID, title: "TITLE-SECRET", body: "BODY-SECRET" });
    expect(w.shown.map((s) => [s.title, s.opts.body])).toEqual([
      ["Antares", "Your reply is ready."],
      ["Antares", "A run did not finish."],
    ]);
    expect(JSON.stringify(w.shown)).not.toMatch(/SECRET/);
  });

  it("stays generic for a payload it cannot read, and never carries an unvetted id into the link", async () => {
    const w = worker();
    await w.push("not json", true);
    await w.push({ v: 2, kind: "completed", chatId: ID });
    await w.push({ v: 1, kind: "toString", chatId: ID });
    await w.push({ v: 1, kind: "completed", chatId: "../admin/users" });
    expect(w.shown).toHaveLength(4);
    for (const s of w.shown) expect(s.opts.body).toMatch(/^(Your reply is ready\.|Open Antares to see what happened\.)$/);
    expect(w.shown[3].opts.data.chatId).toBe("");
  });

  it("opens the owned chat route, or the home page when the id is not an id", async () => {
    const w = worker();
    await w.click(ID);
    await w.click("../../admin");
    expect(w.opened).toEqual([`/chat/${ID}`, "/"]);
    w.setWindow("https://chat.example.test/");
    await w.click(ID);
    expect(w.navigated).toEqual([`/chat/${ID}`]);
  });
});

describe("the worker cannot cache or intercept anything private", () => {
  it("has no fetch handler and no cache use", () => {
    const src = readFileSync(join(__dirname, "..", "public", "sw.js"), "utf8");
    expect(src).not.toMatch(/addEventListener\(\s*["']fetch["']/);
    expect(src).not.toMatch(/\bcaches\b|importScripts|localStorage/);
  });
});

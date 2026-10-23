import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { describe, expect, it, vi } from "vitest";

const origin = "https://app.test";
const paths = ["/offline.html", "/assets/app-hash.js", "/assets/offline-hash.css"];
const source = readFileSync(new URL("../public/sw.js", import.meta.url), "utf8");

function worker() {
  const listeners: Record<string, (event: any) => void> = {};
  const entries = new Map<string, Response>();
  const cache = {
    put: vi.fn(async (path: string, response: Response) => { entries.set(new URL(path, origin).href, response); }),
    match: vi.fn(async (request: string | Request) => entries.get(new URL(typeof request === "string" ? request : request.url, origin).href)),
    keys: async () => [...entries.keys()].map(url => new Request(url)),
    delete: vi.fn(async (request: Request) => entries.delete(request.url)),
  };
  const fetch = vi.fn(async () => new Response("public"));
  const caches = { open: vi.fn(async () => cache), keys: async () => ["antares-public-test", "unrelated"], delete: vi.fn() };
  const self = { location: { origin }, addEventListener: (name: string, fn: (event: any) => void) => { listeners[name] = fn; }, clients: { claim: vi.fn() }, skipWaiting: vi.fn() };
  runInNewContext(source.replace("__VERSION__", "test").replace("__PRECACHE__", JSON.stringify(paths)), { self, caches, fetch, URL, Response, Set });
  const fire = async (name: string, extra = {}) => {
    let pending: Promise<unknown> | undefined;
    const respondWith = vi.fn((promise: Promise<unknown>) => { pending = promise; });
    listeners[name]({ waitUntil: (promise: Promise<unknown>) => { pending = promise; }, respondWith, ...extra });
    return { respondWith, result: await pending };
  };
  return { entries, cache, fetch, caches, self, fire };
}

describe("public-only service worker", () => {
  it("precaches only the build list anonymously; never saves navigation responses", async () => {
    const w = worker();
    await w.fire("install");
    expect(w.cache.put.mock.calls.map(([path]) => path)).toEqual(paths);
    expect(w.fetch).toHaveBeenCalledWith("/offline.html", { credentials: "omit", cache: "reload" });
    w.cache.put.mockClear();
    await w.fire("fetch", { request: { url: `${origin}/chat/private`, method: "GET", mode: "navigate" } });
    expect(w.cache.put).not.toHaveBeenCalled();
  });

  it("never intercepts or caches any API, admin, foreign-origin, query-string or write response", async () => {
    const w = worker();
    for (const path of ["/api", "/api/me", "/api/chats", "/api/chats/secret/messages", "/api/admin/users", "/API/me", "/api/assets/private", "/assets/app-hash.js?private=1", "https://other.test/assets/app-hash.js"]) {
      for (const mode of ["navigate", "cors"]) {
        if (mode === "navigate" && path.includes("?")) continue;
        const { respondWith } = await w.fire("fetch", { request: { url: new URL(path, origin).href, method: "GET", mode } });
        expect(respondWith, path).not.toHaveBeenCalled();
      }
    }
    await w.fire("fetch", { request: { url: `${origin}/assets/app-hash.js`, method: "POST" } });
    expect(w.fetch).not.toHaveBeenCalled();
    expect(w.caches.open).not.toHaveBeenCalled();
    expect(w.cache.put).not.toHaveBeenCalled();
  });

  it("returns only the anonymous shell offline, and never caches runtime data", async () => {
    const w = worker();
    await w.fire("install");
    w.fetch.mockRejectedValue(new Error("offline"));
    const { result } = await w.fire("fetch", { request: { url: `${origin}/chats/private`, method: "GET", mode: "navigate" } });
    expect(result).toBe(w.entries.get(`${origin}/offline.html`));
    expect(w.cache.put).toHaveBeenCalledTimes(paths.length);
  });

  it("refuses failed, redirected and no-store precache responses", async () => {
    for (const response of [new Response("private", { headers: { "Cache-Control": "no-store" } }), new Response("error", { status: 500 }), Object.assign(new Response("redirect"), {})]) {
      if (response.status === 200 && !response.headers.has("Cache-Control")) Object.defineProperty(response, "redirected", { value: true });
      const w = worker();
      w.fetch.mockResolvedValue(response);
      await expect(w.fire("install")).rejects.toThrow("Public asset unavailable");
      expect(w.cache.put).not.toHaveBeenCalled();
    }
  });

  it("purges unexpected user-bound entries while retaining public assets", async () => {
    const w = worker();
    await w.fire("install");
    w.entries.set(`${origin}/api/me`, new Response("private"));
    await w.fire("message", { data: { type: "PURGE_USER_STATE" } });
    expect([...w.entries.keys()]).toEqual(paths.map(path => `${origin}${path}`));
    expect(w.caches.delete).not.toHaveBeenCalled();
  });

  it("never skips waiting on install; activates only on explicit update action and retires old caches", async () => {
    const w = worker();
    await w.fire("install");
    expect(w.self.skipWaiting).not.toHaveBeenCalled();
    await w.fire("message", { data: { type: "ACTIVATE_UPDATE" } });
    expect(w.self.skipWaiting).toHaveBeenCalledOnce();
    w.caches.keys = async () => ["antares-public-old", "antares-public-test", "unrelated"];
    await w.fire("activate");
    expect(w.caches.delete).toHaveBeenCalledExactlyOnceWith("antares-public-old");
    expect(w.self.clients.claim).toHaveBeenCalledOnce();
  });
});

import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { describe, expect, it, vi } from "vitest";

const source = readFileSync(new URL("../public/sw.js", import.meta.url), "utf8").replace("__VERSION__", "test").replace("__PRECACHE__", "[]");

function worker(fetchImpl: (url: string, init?: any) => Promise<any>, subscribe = vi.fn()) {
  const listeners: Record<string, (event: any) => void> = {};
  const self = {
    location: { origin: "https://app.test" },
    addEventListener: (name: string, fn: (event: any) => void) => { listeners[name] = fn; },
    registration: { pushManager: { getSubscription: vi.fn(async () => null), subscribe } },
  };
  const fetch = vi.fn<(url: string, init?: any) => Promise<any>>(fetchImpl);
  const console = { error: vi.fn() };
  runInNewContext(source, { self, fetch, console, URL, Response, Set, atob, Uint8Array });
  const fire = async (event: object) => {
    let pending: Promise<unknown> | undefined;
    listeners["pushsubscriptionchange"]({ waitUntil: (p: Promise<unknown>) => { pending = p; }, ...event });
    await pending;
  };
  return { fetch, console, fire, subscribe };
}

const newSub = (endpoint: string) => ({ endpoint, toJSON: () => ({ endpoint, keys: { p256dh: "P", auth: "A" } }) });
const KEY = new Uint8Array([1, 2, 3]);
const old = { options: { applicationServerKey: KEY.buffer } };
const ok = () => new Response(JSON.stringify({ id: "x" }), { status: 201 });

describe("pushsubscriptionchange", () => {
  it("re-subscribes with the old application server key and POSTs the new endpoint with the session cookie", async () => {
    const subscribe = vi.fn(async () => newSub("https://push.test/new"));
    const w = worker(async () => ok(), subscribe);
    await w.fire({ oldSubscription: old });
    expect(subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: KEY.buffer });
    expect(w.fetch).toHaveBeenCalledTimes(1);
    const [url, init] = w.fetch.mock.calls[0];
    expect(url).toBe("/api/push/subscriptions");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("include");
    expect(JSON.parse(init.body)).toEqual({ endpoint: "https://push.test/new", keys: { p256dh: "P", auth: "A" } });
    expect(w.console.error).not.toHaveBeenCalled();
  });

  it("uses the browser's newSubscription when it supplies one", async () => {
    const w = worker(async () => ok());
    await w.fire({ oldSubscription: old, newSubscription: newSub("https://push.test/given") });
    expect(w.subscribe).not.toHaveBeenCalled();
    expect(JSON.parse(w.fetch.mock.calls[0][1].body).endpoint).toBe("https://push.test/given");
  });

  it("fetches the public key when the old subscription is gone", async () => {
    const subscribe = vi.fn(async (_: any) => newSub("https://push.test/new"));
    const w = worker(async (url) => (url === "/api/push/public-key" ? new Response(JSON.stringify({ publicKey: "AQID" })) : ok()), subscribe);
    await w.fire({ oldSubscription: null });
    expect(w.fetch.mock.calls[0][0]).toBe("/api/push/public-key");
    expect(w.fetch.mock.calls[0][1].credentials).toBe("include");
    expect([...subscribe.mock.calls[0][0].applicationServerKey]).toEqual([1, 2, 3]);
    expect(w.fetch.mock.calls[1][0]).toBe("/api/push/subscriptions");
  });

  it("logs a refused re-register instead of swallowing it", async () => {
    const w = worker(async () => new Response("{}", { status: 401 }), vi.fn(async () => newSub("https://push.test/new")));
    await w.fire({ oldSubscription: old });
    expect(w.console.error).toHaveBeenCalledTimes(1);
    expect(String(w.console.error.mock.calls[0][1])).toMatch(/HTTP 401/);
  });

  it("logs a failed re-subscribe", async () => {
    const w = worker(async () => ok(), vi.fn(async () => { throw new Error("denied"); }));
    await w.fire({ oldSubscription: old });
    expect(w.fetch).not.toHaveBeenCalled();
    expect(w.console.error).toHaveBeenCalledTimes(1);
  });
});

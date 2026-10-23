// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { enable } from "./push";

const subscription = (endpoint: string) => ({
  endpoint,
  toJSON: () => ({ keys: { p256dh: "pk", auth: "au" } }),
  getKey: () => null,
  unsubscribe: vi.fn(async () => true),
});

describe("enable() with an endpoint another account holds", () => {
  const posted: string[] = [];
  let subscribe: ReturnType<typeof vi.fn>;
  let existing: ReturnType<typeof subscription>;

  beforeEach(() => {
    posted.length = 0;
    existing = subscription("https://push.example.test/old");
    subscribe = vi.fn(async () => subscription("https://push.example.test/new"));
    const reg = { pushManager: { getSubscription: async () => existing, subscribe } };
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("Notification", Object.assign(class {}, { permission: "granted", requestPermission: async () => "granted" }));
    Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
    Object.defineProperty(navigator, "serviceWorker", { value: { register: async () => reg, ready: Promise.resolve(reg) }, configurable: true });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url === "/api/push/public-key") return new Response(JSON.stringify({ publicKey: "AAAA" }), { status: 200 });
        const ep = JSON.parse(String(init?.body)).endpoint as string;
        posted.push(ep);
        return ep.endsWith("/old")
          ? new Response(JSON.stringify({ error: { code: "ENDPOINT_IN_USE" } }), { status: 409 })
          : new Response(JSON.stringify({ id: "d2" }), { status: 201 });
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it("drops the refused subscription and registers a fresh one, once", async () => {
    const r = await enable();
    expect(r).toEqual({ ok: true, value: true });
    expect(existing.unsubscribe).toHaveBeenCalledTimes(1);
    expect(subscribe).toHaveBeenCalledTimes(1);
    expect(posted).toEqual(["https://push.example.test/old", "https://push.example.test/new"]);
  });

  it("gives up cleanly when the fresh subscription is refused too", async () => {
    subscribe.mockResolvedValue(subscription("https://push.example.test/old"));
    const r = await enable();
    expect(r).toEqual({ ok: false, code: "ENDPOINT_IN_USE" });
    expect(posted).toHaveLength(2);
  });
});

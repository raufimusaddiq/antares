// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import { useState } from "react";
import { DraftNotice, useDraftSync } from "./DraftSync";
import { sharedText } from "./draftApi";
import { mergeTemplates, matchTemplates, TEMPLATES } from "./drafts";

// A fake server that enforces the same If-Match rule as the real one, per draft path.
function fakeServer() {
  const drafts = new Map<string, { body: string; revision: number }>();
  const log: string[] = [];
  let down = false;
  const f = vi.fn(async (path: string, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    log.push(`${method} ${path}`);
    if (down) throw new TypeError("down");
    const cur = drafts.get(path) ?? { body: "", revision: 0 };
    const reply = (status: number, body?: unknown) => ({ status, ok: status < 400, json: async () => body ?? null }) as Response;
    if (method === "GET") return reply(200, cur);
    const im = Number(new Headers(init?.headers).get("If-Match")?.replaceAll('"', ""));
    if (im !== cur.revision) return reply(409, { error: { code: "REVISION_CONFLICT" }, current: cur });
    if (method === "DELETE") {
      drafts.delete(path);
      return reply(204);
    }
    const next = { body: JSON.parse(String(init?.body)).body as string, revision: cur.revision + 1 };
    drafts.set(path, next);
    return reply(200, next);
  });
  return { f, drafts, log, setDown: (d: boolean) => (down = d) };
}

let srv: ReturnType<typeof fakeServer>;
beforeEach(() => {
  vi.useFakeTimers();
  srv = fakeServer();
  vi.stubGlobal("fetch", srv.f);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const settle = () => act(async () => void (await vi.advanceTimersByTimeAsync(1000)));

let api: { setDraft: (v: string) => void; setChat: (c: string | null) => void; text: () => string; sync: ReturnType<typeof useDraftSync> };
function Device({ chat = "c1", initial = "", share = null }: { chat?: string | null; initial?: string; share?: string | null }) {
  const [c, setC] = useState<string | null>(chat);
  const [d, setD] = useState(initial);
  const sync = useDraftSync(c, d, setD, share, () => {});
  api = { setDraft: setD, setChat: (n) => { setC(n); setD(""); },  text: () => d, sync };
  return <DraftNotice sync={sync} />;
}
const mount = async (p: Parameters<typeof Device>[0] = {}) => {
  const r = render(<Device {...p} />);
  await settle();
  return r;
};
const P1 = "/api/chats/c1/draft";

describe("server draft sync", () => {
  it("a draft typed on one device restores on another", async () => {
    const a = await mount();
    await act(async () => api.setDraft("half a thought"));
    await settle();
    expect(srv.drafts.get(P1)).toEqual({ body: "half a thought", revision: 1 });
    a.unmount();
    await mount();
    expect(api.text()).toBe("half a thought");
  });

  it("a stale device never overwrites newer server text: it pauses and asks", async () => {
    await mount({ initial: "phone text" }); // this device has unsynced text; revision 0 -> pushes
    await settle();
    expect(srv.drafts.get(P1)?.revision).toBe(1);
    srv.drafts.set(P1, { body: "laptop text", revision: 2 }); // another device wrote meanwhile
    await act(async () => api.setDraft("phone text, more"));
    await settle();
    expect(srv.drafts.get(P1)).toEqual({ body: "laptop text", revision: 2 }); // untouched
    expect(api.sync.conflict).toEqual({ theirs: "laptop text" });
    expect(api.text()).toBe("phone text, more"); // my text is not thrown away either
    const puts = srv.log.filter((l) => l.startsWith("PUT")).length;
    await act(async () => api.setDraft("phone text, even more"));
    await settle();
    expect(srv.log.filter((l) => l.startsWith("PUT")).length).toBe(puts); // paused: no further writes
    await act(async () => api.sync.keepMine());
    await settle();
    expect(srv.drafts.get(P1)).toEqual({ body: "phone text, even more", revision: 3 });
    expect(api.sync.conflict).toBeNull();
  });

  it("'use the other device's text' replaces the box and writes nothing", async () => {
    srv.drafts.set(P1, { body: "server text", revision: 4 });
    await mount({ initial: "local text" });
    expect(api.sync.conflict).toEqual({ theirs: "server text" });
    await act(async () => api.sync.useTheirs());
    await settle();
    expect(api.text()).toBe("server text");
    expect(srv.drafts.get(P1)).toEqual({ body: "server text", revision: 4 });
  });

  it("leaving a chat saves its text to that chat, not the next one", async () => {
    await mount();
    await act(async () => api.setDraft("for chat one"));
    await act(async () => api.setChat("c2")); // before the debounce fires
    await settle();
    expect(srv.drafts.get(P1)?.body).toBe("for chat one");
    expect(srv.drafts.get("/api/chats/c2/draft")).toBeUndefined();
  });

  it("emptying the box (a send) deletes the server draft at its revision", async () => {
    await mount();
    await act(async () => api.setDraft("to send"));
    await settle();
    await act(async () => api.setDraft(""));
    await settle();
    expect(srv.drafts.has(P1)).toBe(false);
  });

  it("an unreachable server is said out loud and the text stays", async () => {
    srv.setDown(true);
    const r = await mount({ initial: "kept" });
    expect(r.container.textContent).toContain("saved in this browser only");
    expect(api.text()).toBe("kept");
  });
});

describe("share to draft", () => {
  it("parses only a share navigation, joins title/text/url, drops duplicates", () => {
    expect(sharedText("")).toBeNull();
    expect(sharedText("?text=hi")).toBeNull();
    expect(sharedText("?share=1")).toBeNull();
    expect(sharedText("?share=1&title=T&text=body&url=https%3A%2F%2Fx.test")).toBe("T\nbody\nhttps://x.test");
    expect(sharedText("?share=1&text=same&title=same")).toBe("same");
  });

  it("lands in the new-chat draft, appended, and is never sent", async () => {
    srv.drafts.set("/api/me/new-chat-draft", { body: "earlier", revision: 1 });
    await mount({ chat: null, share: "shared words" });
    await settle();
    expect(api.text()).toBe("earlier\n\nshared words");
    expect(srv.drafts.get("/api/me/new-chat-draft")?.body).toBe("earlier\n\nshared words");
    expect(srv.log.some((l) => l.includes("/messages"))).toBe(false);
    expect(srv.log.some((l) => l.startsWith("POST"))).toBe(false);
  });
});

describe("template merge", () => {
  it("own templates win by name, are matchable, and insert as text", () => {
    const own = [{ name: "summarize", body: "mine" }, { name: "my-1_x", body: "/run rm -rf /" }];
    const all = mergeTemplates(own);
    expect(all.filter((t) => t.name === "summarize")).toEqual([{ name: "summarize", body: "mine" }]);
    expect(all).toHaveLength(TEMPLATES.length + 1);
    expect(matchTemplates("/my-", all)).toEqual([{ name: "my-1_x", body: "/run rm -rf /" }]);
  });
});

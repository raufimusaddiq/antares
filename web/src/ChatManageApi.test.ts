// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { changeChat, deleteChat, listChats, parseChat } from "./chatApi";

// What the browser sends and accepts for chat management. The browser never names a profile, a session or an
// owner: the path carries the chat's app id and the body carries exactly one change.

const reply = (status: number, body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
const call = (f: ReturnType<typeof reply>) => {
  const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
  return { url, method: init.method, body: init.body as string | undefined, credentials: init.credentials };
};
const chatBody = { id: "c1", title: "T", createdAt: "a", updatedAt: "b", activeRunId: "", pinned: true, archived: false };

afterEach(() => vi.unstubAllGlobals());

describe("changeChat", () => {
  it("sends one change by PATCH to the chat's own path and reads the chat back", async () => {
    const f = reply(200, chatBody);
    vi.stubGlobal("fetch", f);
    expect(await changeChat("c 1/x", { pinned: true })).toMatchObject({ ok: true, value: { id: "c1", pinned: true, archived: false, deleteFailed: false } });
    expect(call(f)).toEqual({ url: "/api/chats/c%201%2Fx", method: "PATCH", body: '{"pinned":true}', credentials: "same-origin" });
  });

  it("maps a refusal to its code, not the server's words", async () => {
    vi.stubGlobal("fetch", reply(409, { error: { code: "CHAT_STATE", message: "SECRET" } }));
    expect(await changeChat("c1", { archived: true })).toEqual({ ok: false, code: "CHAT_STATE", status: 409 });
  });

  it("does not accept an answer that is not a chat", async () => {
    vi.stubGlobal("fetch", reply(200, { ok: true }));
    expect(await changeChat("c1", { title: "x" })).toMatchObject({ ok: false });
  });
});

describe("deleteChat", () => {
  it("is a DELETE on the chat's path and succeeds only when the server says deleted:true", async () => {
    const f = reply(200, { deleted: true, id: "c1" });
    vi.stubGlobal("fetch", f);
    expect(await deleteChat("c1")).toEqual({ ok: true, value: { deleted: true } });
    expect(call(f)).toMatchObject({ url: "/api/chats/c1", method: "DELETE", body: undefined });
  });

  it("a 200 without the confirmation is a failure, never a guessed success", async () => {
    for (const body of [{}, { deleted: false }, { deleted: "true" }, []]) {
      vi.stubGlobal("fetch", reply(200, body));
      expect(await deleteChat("c1"), JSON.stringify(body)).toMatchObject({ ok: false });
    }
  });

  it("keeps the server's refusal codes", async () => {
    for (const [status, code] of [[409, "RUN_CONFLICT"], [503, "HERMES_UNAVAILABLE"], [501, "CAPABILITY_UNAVAILABLE"], [404, "NOT_FOUND"]] as const) {
      vi.stubGlobal("fetch", reply(status, { error: { code } }));
      expect(await deleteChat("c1")).toEqual({ ok: false, code, status });
    }
  });
});

describe("the list", () => {
  it("asks for the archive explicitly and keeps the cursor", async () => {
    const f = reply(200, { chats: [], nextCursor: "" });
    vi.stubGlobal("fetch", f);
    await listChats(undefined, true);
    expect(call(f).url).toBe("/api/chats?archived=1");
    const g = reply(200, { chats: [], nextCursor: "" });
    vi.stubGlobal("fetch", g);
    await listChats("p 1", true);
    expect(call(g).url).toBe("/api/chats?cursor=p%201&archived=1");
    const h = reply(200, { chats: [], nextCursor: "" });
    vi.stubGlobal("fetch", h);
    await listChats();
    expect(call(h).url).toBe("/api/chats");
  });

  it("reads the flags strictly: anything but true is false", () => {
    expect(parseChat({ ...chatBody, pinned: "yes", archived: 1, deleteFailed: "true" })).toMatchObject({ pinned: false, archived: false, deleteFailed: false });
    expect(parseChat({ ...chatBody, deleteFailed: true })).toMatchObject({ deleteFailed: true });
  });
});

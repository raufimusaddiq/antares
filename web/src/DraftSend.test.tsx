// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";
import { saveDraft } from "./drafts";

const api = vi.hoisted(() => ({
  listChats: vi.fn(),
  createChat: vi.fn(),
  getMessages: vi.fn(),
  sendMessage: vi.fn(),
  getRun: vi.fn(),
  cancelRun: vi.fn(),
}));

vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const chat = (id: string, extra: Partial<ChatSummary> = {}): ChatSummary => ({ id, title: "", createdAt: "", updatedAt: "", activeRunId: "", ...extra });
const ok = <T,>(value: T) => ({ ok: true as const, value });
const page = (texts: string[] = []) => ok({ turns: texts.map((text, seq) => ({ seq, role: seq % 2 ? "assistant" : "user", text })), nextCursor: "", total: texts.length });

class FakeSource {
  static all: FakeSource[] = [];
  static CLOSED = 2;
  readyState = 1;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeSource.all.push(this);
  }
  addEventListener() {}
  close() {
    this.closed = true;
    this.readyState = 2;
  }
}

const mount = async (chats: ChatSummary[]) => {
  api.listChats.mockResolvedValue(ok({ chats, nextCursor: "" }));
  render(<Chat email="a@example.test" onSignOut={() => {}} />);
  await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
};

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeSource);
  api.getMessages.mockResolvedValue(page());
  api.getRun.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
});
afterEach(() => {
  cleanup();
  FakeSource.all = [];
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});


function draftServer() {
  const calls: string[] = [];
  const docs = new Map<string, { body: string; revision: number }>();
  let gate: Promise<void> | null = null; // holds the new-chat draft's GET
  let failGets = false;
  const f = vi.fn(async (path: string, init?: RequestInit) => {
    const m = init?.method ?? "GET";
    const reply = (status: number, json: unknown) => ({ status, ok: status < 400, json: async () => json }) as Response;
    if (!path.includes("draft")) return reply(404, null);
    calls.push(`${m} ${path}`);
    const cur = docs.get(path) ?? { body: "", revision: 0 };
    if (m === "GET") {
      if (failGets && path.includes("new-chat-draft")) throw new TypeError("down");
      if (gate && path.includes("new-chat-draft")) await gate;
      return reply(200, docs.get(path) ?? { body: "", revision: 0 });
    }
    const ifMatch = Number(new Headers(init?.headers).get("If-Match")?.replaceAll('"', ""));
    if (ifMatch !== cur.revision) return reply(409, { error: { code: "REVISION_CONFLICT" }, current: cur });
    const next = m === "PUT" ? { body: JSON.parse(String(init?.body)).body as string, revision: cur.revision + 1 } : { body: "", revision: cur.revision + 1 };
    docs.set(path, next);
    return reply(200, next);
  });
  return {
    f,
    calls,
    docs,
    gateNewChatGet: () => {
      let open!: () => void;
      gate = new Promise<void>((r) => (open = r));
      return () => {
        gate = null;
        open();
      };
    },
    setFailGets: (v: boolean) => (failGets = v),
  };
}

describe("the server draft outlives a send that was not admitted (PRD 'cleared only after durable admission')", () => {
  it("is deleted only once the send is admitted", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    await mount([chat("c1")]);
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "my message" } });
    await waitFor(() => expect(srv.calls.some((c) => c.startsWith("PUT"))).toBe(true), { timeout: 3000 });

    api.sendMessage.mockResolvedValueOnce({ ok: false, code: "UPSTREAM_UNAVAILABLE" });
    fireEvent.submit(document.querySelector("form.composer")!);
    await screen.findByText("Send again");
    await new Promise((r) => setTimeout(r, 1200)); // longer than the sync debounce
    expect(srv.calls.some((c) => c.startsWith("DELETE"))).toBe(false); // not admitted: the draft is still on the server

    api.sendMessage.mockResolvedValueOnce(ok({ runId: "r9", status: "PENDING" }));
    fireEvent.click(screen.getByText("Send again"));
    await waitFor(() => expect(srv.calls.some((c) => c.startsWith("DELETE"))).toBe(true), { timeout: 3000 });
  });

  it("after a failed send, new text typed in the composer reaches the server", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    await mount([chat("c1")]);
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "my message" } });
    await waitFor(() => expect(srv.calls.filter((c) => c.startsWith("PUT")).length).toBe(1), { timeout: 3000 });
    api.sendMessage.mockResolvedValueOnce({ ok: false, code: "UPSTREAM_UNAVAILABLE" });
    fireEvent.submit(document.querySelector("form.composer")!);
    await screen.findByText("Send again");
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "a better message" } });
    await waitFor(() => expect(srv.calls.filter((c) => c.startsWith("PUT")).length).toBe(2), { timeout: 3000 });
    expect(srv.calls.some((c) => c.startsWith("DELETE"))).toBe(false);
  });

  it("after a failed chat creation, the new-chat draft keeps syncing", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    await mount([]);
    api.createChat.mockResolvedValueOnce({ ok: false, code: "UPSTREAM_UNAVAILABLE" });
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "first words" } });
    await waitFor(() => expect(srv.calls.filter((c) => c.startsWith("PUT")).length).toBe(1), { timeout: 3000 });
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.createChat).toHaveBeenCalled());
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "second words" } });
    await waitFor(() => expect(srv.calls.filter((c) => c.startsWith("PUT")).length).toBe(2), { timeout: 3000 });
    expect(srv.calls.some((c) => c.startsWith("DELETE"))).toBe(false);
  });
});

// A new-chat send replaces the draft scope (null -> the created chat) BEFORE admission returns. The abandoned
// scope's server draft must still end up cleared, and another device's newer edit must survive.
describe("a send from the new-chat composer clears the new-chat server draft even though the scope changes", () => {
  const NEW = "/api/me/new-chat-draft";
  const sendFromNewChat = async (srv: ReturnType<typeof draftServer>) => {
    saveDraft("a@example.test", null, "saved text"); // this browser's copy, shown in the box while the read is pending
    api.createChat.mockResolvedValue(ok(chat("c9")));
    api.sendMessage.mockResolvedValue(ok({ runId: "r1", status: "PENDING" }));
    await mount([]);
    expect((document.getElementById("composer-input") as HTMLTextAreaElement).value).toBe("saved text");
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    void srv;
  };

  it("delayed first read resolving after a successful send leaves a tombstone", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    srv.docs.set(NEW, { body: "saved text", revision: 1 });
    const open = srv.gateNewChatGet();
    await sendFromNewChat(srv);
    await act(async () => open());
    await waitFor(() => expect(srv.docs.get(NEW)).toEqual({ body: "", revision: 2 }), { timeout: 3000 });
  });

  it("a newer edit from another device survives", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    srv.docs.set(NEW, { body: "written on the laptop", revision: 3 });
    const open = srv.gateNewChatGet();
    await sendFromNewChat(srv);
    await act(async () => open());
    await new Promise((r) => setTimeout(r, 1200));
    expect(srv.docs.get(NEW)).toEqual({ body: "written on the laptop", revision: 3 });
    expect(srv.calls.some((c) => c.startsWith("DELETE"))).toBe(false);
  });

  it("an initial read that failed before the scope change is retried on reconnect", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    srv.docs.set(NEW, { body: "saved text", revision: 1 });
    srv.setFailGets(true);
    await sendFromNewChat(srv);
    await new Promise((r) => setTimeout(r, 300));
    expect(srv.docs.get(NEW)).toEqual({ body: "saved text", revision: 1 }); // could not be cleared yet
    srv.setFailGets(false);
    await act(async () => void window.dispatchEvent(new Event("online")));
    await waitFor(() => expect(srv.docs.get(NEW)).toEqual({ body: "", revision: 2 }), { timeout: 3000 });
  });

  it("leaving the page before the read returns never resurrects the sent text into the box", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    srv.docs.set(NEW, { body: "saved text", revision: 1 });
    const open = srv.gateNewChatGet();
    await sendFromNewChat(srv);
    cleanup(); // navigation / unmount with the first read still in flight
    open();
    await new Promise((r) => setTimeout(r, 300));
    await mount([]); // the next visit: the durable cleared marker, not the sent text, decides
    await new Promise((r) => setTimeout(r, 300));
    expect((document.getElementById("composer-input") as HTMLTextAreaElement).value).toBe("");
    expect(srv.calls.filter((c) => c.startsWith("PUT")).length).toBe(0); // nothing was written back
  });
});

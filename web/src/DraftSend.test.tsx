// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";
import { loadDraft, saveDraft } from "./drafts";

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

const chat = (id: string, extra: Partial<ChatSummary> = {}): ChatSummary => ({ id, title: "", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false, ...extra });
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
  FakeSource.all = [];
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

// Each submission owns its own hold: overlapping sends in two chats must not release one another's.
describe("overlapping sends in different chats", () => {
  const C1 = "/api/chats/c1/draft";
  const C2 = "/api/chats/c2/draft";
  const text = () => (document.getElementById("composer-input") as HTMLTextAreaElement).value;
  const openSecond = () => fireEvent.click(document.querySelectorAll("nav.side li button.item")[1]);

  const setup = async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    srv.docs.set(C1, { body: "draft one", revision: 1 });
    srv.docs.set(C2, { body: "draft two", revision: 1 });
    saveDraft("a@example.test", "c1", "draft one");
    saveDraft("a@example.test", "c2", "draft two");
    const answers: Record<string, (v: unknown) => void> = {};
    api.sendMessage.mockImplementation((chatId: string) => new Promise((r) => (answers[chatId] = r)));
    await mount([chat("c1"), chat("c2")]);
    await waitFor(() => expect(text()).toBe("draft one"));
    fireEvent.submit(document.querySelector("form.composer")!); // send A in c1
    await waitFor(() => expect(answers.c1).toBeDefined());
    openSecond(); // navigate to c2 before A answers
    await waitFor(() => expect(text()).toBe("draft two"));
    fireEvent.submit(document.querySelector("form.composer")!); // send B in c2
    await waitFor(() => expect(answers.c2).toBeDefined());
    return { srv, answers };
  };
  const admitted = ok({ runId: "r1", status: "PENDING" });
  const refused = { ok: false, code: "UPSTREAM_UNAVAILABLE" };

  for (const order of [["c1", "c2"], ["c2", "c1"]]) {
    it(`A admitted, B refused, answers in order ${order.join(" then ")}: A's draft is cleared, B's is kept and both holds end`, async () => {
      const { srv, answers } = await setup();
      for (const id of order) await act(async () => answers[id](id === "c1" ? admitted : refused));
      await waitFor(() => expect(srv.docs.get(C1)).toEqual({ body: "", revision: 2 }), { timeout: 3000 });
      await new Promise((r) => setTimeout(r, 1200));
      expect(srv.docs.get(C2)).toEqual({ body: "draft two", revision: 1 }); // refused: not deleted
      expect(srv.calls.filter((c) => c.startsWith("DELETE") && c.includes("/c2/")).length).toBe(0);
      // B's hold ended: what is typed next in c2 reaches the server.
      fireEvent.change(document.getElementById("composer-input")!, { target: { value: "typed after the refusal" } });
      await waitFor(() => expect(srv.docs.get(C2)).toEqual({ body: "typed after the refusal", revision: 2 }), { timeout: 3000 });
    });

    it(`A refused, B admitted, answers in order ${order.join(" then ")}: B's draft is cleared, A's is kept and both holds end`, async () => {
      const { srv, answers } = await setup();
      for (const id of order) await act(async () => answers[id](id === "c2" ? admitted : refused));
      await waitFor(() => expect(srv.docs.get(C2)).toEqual({ body: "", revision: 2 }), { timeout: 3000 });
      await new Promise((r) => setTimeout(r, 1200));
      expect(srv.docs.get(C1)).toEqual({ body: "draft one", revision: 1 });
      expect(srv.calls.filter((c) => c.startsWith("DELETE") && c.includes("/c1/")).length).toBe(0);
      // A's hold ended too: go back to c1 and type; it reaches the server.
      fireEvent.click(document.querySelectorAll("nav.side li button.item")[0]);
      await waitFor(() => expect(text()).toBe("draft one"));
      fireEvent.change(document.getElementById("composer-input")!, { target: { value: "back in one" } });
      await waitFor(() => expect(srv.docs.get(C1)).toEqual({ body: "back in one", revision: 2 }), { timeout: 3000 });
    });
  }

  it("a resend of a new-chat send clears the new-chat draft it came from", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    srv.docs.set("/api/me/new-chat-draft", { body: "saved text", revision: 1 });
    saveDraft("a@example.test", null, "saved text");
    api.createChat.mockResolvedValue(ok(chat("c9")));
    api.sendMessage.mockResolvedValueOnce(refused);
    await mount([]);
    await waitFor(() => expect(text()).toBe("saved text"));
    await waitFor(() => expect(srv.calls.some((c) => c.startsWith("GET /api/me/new-chat-draft"))).toBe(true));
    fireEvent.submit(document.querySelector("form.composer")!);
    await screen.findByText("Send again");
    expect(srv.docs.get("/api/me/new-chat-draft")).toEqual({ body: "saved text", revision: 1 }); // refused: kept
    api.sendMessage.mockResolvedValueOnce(admitted);
    fireEvent.click(screen.getByText("Send again"));
    await waitFor(() => expect(srv.docs.get("/api/me/new-chat-draft")).toEqual({ body: "", revision: 2 }), { timeout: 3000 });
  });
});

// Input durability when a send fails BEFORE it is admitted and before any autosave has run.
describe("a message is never lost to a failure before admission", () => {
  const NEW = "/api/me/new-chat-draft";
  const box = () => document.getElementById("composer-input") as HTMLTextAreaElement;
  const refusedCreate = { ok: false, code: "UPSTREAM_UNAVAILABLE" };

  it("submitting at once and failing to create the chat puts the whole text back", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    api.createChat.mockResolvedValueOnce(refusedCreate);
    await mount([]);
    fireEvent.change(box(), { target: { value: "a long message typed in one go" } });
    fireEvent.submit(document.querySelector("form.composer")!); // before the 800 ms autosave
    await waitFor(() => expect(api.createChat).toHaveBeenCalled());
    await waitFor(() => expect(box().value).toBe("a long message typed in one go"));
    expect(loadDraft("a@example.test", null)).toBe("a long message typed in one go"); // this browser's copy too
    await waitFor(() => expect(srv.docs.get(NEW)).toEqual({ body: "a long message typed in one go", revision: 1 }), { timeout: 3000 });
    expect(srv.calls.some((c) => c.startsWith("DELETE"))).toBe(false);
  });

  it("an unsynced tail beyond an older server copy survives", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    srv.docs.set(NEW, { body: "older", revision: 1 });
    saveDraft("a@example.test", null, "older and then more that was never synced");
    api.createChat.mockResolvedValueOnce(refusedCreate);
    await mount([]);
    await waitFor(() => expect(box().value).toBe("older and then more that was never synced"));
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.createChat).toHaveBeenCalled());
    await waitFor(() => expect(box().value).toBe("older and then more that was never synced"));
    await waitFor(() => expect(srv.docs.get(NEW)?.body).toBe("older and then more that was never synced"), { timeout: 3000 });
  });

  it("text typed while the chat was being created is kept, and the captured message stays recoverable", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    let fail!: (v: unknown) => void;
    api.createChat.mockReturnValueOnce(new Promise((r) => (fail = r)));
    await mount([]);
    fireEvent.change(box(), { target: { value: "captured message" } });
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.createChat).toHaveBeenCalled());
    fireEvent.change(box(), { target: { value: "typed meanwhile" } });
    await act(async () => fail(refusedCreate));
    await waitFor(() => expect(box().value).toBe("captured message\n\ntyped meanwhile"));
  });

  it("a refused send whose chat the reader has left keeps the message in that chat's own draft", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    saveDraft("a@example.test", "c1", "typed in one");
    let answer!: (v: unknown) => void;
    api.sendMessage.mockImplementationOnce(() => new Promise((r) => (answer = r)));
    await mount([chat("c1"), chat("c2")]);
    await waitFor(() => expect(box().value).toBe("typed in one"));
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    fireEvent.click(document.querySelectorAll("nav.side li button.item")[1]); // away before the answer
    await waitFor(() => expect(box().value).toBe(""));
    await act(async () => answer(refusedCreate));
    expect(loadDraft("a@example.test", "c1")).toBe("typed in one"); // not lost
    fireEvent.click(document.querySelectorAll("nav.side li button.item")[0]);
    await waitFor(() => expect(box().value).toBe("typed in one"));
  });

  it("does not overwrite a draft the other chat already has", async () => {
    const srv = draftServer();
    vi.stubGlobal("fetch", srv.f);
    saveDraft("a@example.test", "c1", "typed in one");
    let answer!: (v: unknown) => void;
    api.sendMessage.mockImplementationOnce(() => new Promise((r) => (answer = r)));
    await mount([chat("c1"), chat("c2")]);
    await waitFor(() => expect(box().value).toBe("typed in one"));
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    fireEvent.click(document.querySelectorAll("nav.side li button.item")[1]);
    await waitFor(() => expect(box().value).toBe(""));
    saveDraft("a@example.test", "c1", "something newer typed elsewhere");
    await act(async () => answer(refusedCreate));
    expect(loadDraft("a@example.test", "c1")).toBe("something newer typed elsewhere");
  });
});


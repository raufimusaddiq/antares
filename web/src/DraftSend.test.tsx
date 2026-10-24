// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";

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
  let rev = 0;
  let text = "";
  const f = vi.fn(async (path: string, init?: RequestInit) => {
    const m = init?.method ?? "GET";
    const reply = (status: number, json: unknown) => ({ status, ok: status < 400, json: async () => json }) as Response;
    if (!path.includes("draft")) return reply(404, null);
    calls.push(`${m} ${path}`);
    if (m === "PUT") {
      text = JSON.parse(String(init?.body)).body;
      rev += 1;
    } else if (m === "DELETE") {
      text = "";
      rev += 1;
    }
    return reply(200, { body: text, revision: rev });
  });
  return { f, calls };
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
});

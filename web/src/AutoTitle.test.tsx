// @vitest-environment jsdom
// Automatic chat titles (like ChatGPT): the server titles a chat from its first message while the reply is written;
// the list shows that title when the reply ends, reading the chat once more if the title came later.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import type { ChatQueue, ChatSummary, Inventory } from "./chatApi";

const api = vi.hoisted(() => ({
  listChats: vi.fn(),
  getChat: vi.fn(),
  getMessages: vi.fn(),
  getRun: vi.fn(),
  sendMessage: vi.fn(),
  steerRun: vi.fn(),
  getCapabilities: vi.fn(),
  getQueue: vi.fn(),
  enqueueMessage: vi.fn(),
  editQueued: vi.fn(),
  removeQueued: vi.fn(),
  pauseQueue: vi.fn(),
  sendQueuedNext: vi.fn(),
}));

vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const ok = <T,>(value: T) => ({ ok: true as const, value });
const chat = (extra: Partial<ChatSummary> = {}): ChatSummary => ({ id: "c1", title: "Talk", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false, ...extra });
const inventory: Inventory = { capabilities: [], toolsets: [], providers: [], skills: [] };
const queued = (): ChatQueue => ({ items: [], paused: "", started: null });

class FakeSource {
  static CLOSED = 2;
  static all: FakeSource[] = [];
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  listeners = new Map<string, ((m: { data: string }) => void)[]>();
  constructor(public url: string) {
    FakeSource.all.push(this);
  }
  addEventListener(name: string, fn: (m: { data: string }) => void) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), fn]);
  }
  emit(name: string, data: unknown) {
    for (const fn of this.listeners.get(name) ?? []) fn({ data: JSON.stringify(data) });
  }
  close() {
    this.readyState = 2;
  }
}


const mount = async (c: ChatSummary) => {
  api.listChats.mockResolvedValue(ok({ chats: [c], nextCursor: "" }));
  render(<Chat email="a@example.test" onSignOut={() => {}} />);
  await waitFor(() => expect(api.getQueue).toHaveBeenCalled());
};

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeSource);
  api.getCapabilities.mockResolvedValue(ok(inventory));
  api.getMessages.mockResolvedValue(ok({ turns: [{ seq: 0, role: "user", text: "first question" }], nextCursor: "", total: 1 }));
  api.getRun.mockResolvedValue(ok({ runId: "r1", status: "RUNNING" }));
  api.getQueue.mockResolvedValue(ok(queued()));
});
afterEach(() => {
  cleanup();
  FakeSource.all = [];
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});

describe("automatic chat title", () => {
  it("shows the title the server wrote once the first reply completes", async () => {
    await mount(chat({ title: "", activeRunId: "r1" }));
    await screen.findByText("Working…");
    expect(screen.getAllByText("New chat").length).toBeGreaterThan(0);
    api.getChat.mockResolvedValue(ok(chat({ title: "Budget Student Laptop" })));
    const run = FakeSource.all.find((s) => s.url.includes("/r1/"))!;
    await act(async () => run.emit("run.completed", { status: "COMPLETED", output: "done" }));
    expect((await screen.findAllByText("Budget Student Laptop")).length).toBeGreaterThan(0);
  });

  it("reads the chat once more when the reply ended before the title was written", async () => {
    await mount(chat({ title: "", activeRunId: "r1" }));
    await screen.findByText("Working…");
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      api.getChat.mockResolvedValueOnce(ok(chat({ title: "" }))).mockResolvedValue(ok(chat({ title: "Resep Rendang Daging Sapi" })));
      const run = FakeSource.all.find((s) => s.url.includes("/r1/"))!;
      await act(async () => run.emit("run.completed", { status: "COMPLETED", output: "done" }));
      await waitFor(() => expect(api.getChat).toHaveBeenCalledTimes(1));
      expect(screen.queryByText("Resep Rendang Daging Sapi")).toBeNull();
      await act(async () => {
        vi.advanceTimersByTime(4000);
      });
      expect((await screen.findAllByText("Resep Rendang Daging Sapi")).length).toBeGreaterThan(0);
      expect(api.getChat).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

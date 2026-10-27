// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
import { describePause } from "./QueuePanel";

const ok = <T,>(value: T) => ({ ok: true as const, value });
const fail = (code: string, status: number) => ({ ok: false as const, code, status });
const chat = (extra: Partial<ChatSummary> = {}): ChatSummary => ({ id: "c1", title: "Talk", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false, ...extra });
const inventory: Inventory = { capabilities: [], toolsets: [], providers: [], skills: [] };
const queued = (inputs: string[], paused = "", started: ChatQueue["started"] = null): ChatQueue => ({
  items: inputs.map((input, i) => ({ id: `q${i + 1}`, position: i + 1, input, sending: false })),
  paused,
  started,
});

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

const box = () => screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
const panel = () => screen.getByRole("region", { name: "Queued messages" });

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
  api.getQueue.mockResolvedValue(ok(queued([])));
});
afterEach(() => {
  cleanup();
  FakeSource.all = [];
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});

describe("queue a message behind a running reply (#110)", () => {
  it("is its own intent: stored on the server, draft cleared, nothing sent or steered", async () => {
    await mount(chat({ activeRunId: "r1" }));
    await screen.findByText("Working…");
    api.enqueueMessage.mockResolvedValue(ok(queued(["and then this"])));
    api.getQueue.mockResolvedValue(ok(queued(["and then this"])));
    fireEvent.change(box(), { target: { value: "and then this" } });
    fireEvent.click(screen.getByRole("button", { name: "Queue" }));
    await waitFor(() => expect(api.enqueueMessage).toHaveBeenCalledWith("c1", "and then this"));
    await waitFor(() => expect(box().value).toBe(""));
    expect(within(panel()).getByText("and then this")).toBeTruthy();
    expect(within(panel()).getByRole("listitem", { name: "Queued message 1 of 1" })).toBeTruthy();
    expect(api.sendMessage).not.toHaveBeenCalled();
    expect(api.steerRun).not.toHaveBeenCalled();
    // Send next waits for the reply to end.
    expect((within(panel()).getByRole("button", { name: "Send next" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("keeps the text and says why when the queue is full", async () => {
    await mount(chat({ activeRunId: "r1" }));
    await screen.findByText("Working…");
    api.enqueueMessage.mockResolvedValue(fail("QUEUE_FULL", 409));
    fireEvent.change(box(), { target: { value: "one more" } });
    fireEvent.click(screen.getByRole("button", { name: "Queue" }));
    expect(await screen.findByText(/queue is full/)).toBeTruthy();
    expect(box().value).toBe("one more");
  });

  it("is not offered when no reply is running", async () => {
    await mount(chat());
    expect(screen.queryByRole("button", { name: "Queue" })).toBeNull();
  });

  it("follows the run the server started from the queue when the reply completes", async () => {
    api.getQueue.mockResolvedValue(ok(queued(["queued follow-up"])));
    await mount(chat({ activeRunId: "r1" }));
    await screen.findByText("Working…");
    await within(panel()).findByText("queued follow-up");
    api.getChat.mockResolvedValue(ok(chat({ activeRunId: "r2" })));
    api.getQueue.mockResolvedValue(ok(queued([])));
    const first = FakeSource.all.find((s) => s.url.includes("/r1/"))!;
    await act(async () => first.emit("run.completed", { status: "COMPLETED", output: "done" }));
    await waitFor(() => expect(FakeSource.all.some((s) => s.url.includes("/r2/"))).toBe(true));
    expect(await screen.findByText("queued follow-up")).toBeTruthy(); // now the user's turn in the transcript
    expect(api.sendMessage).not.toHaveBeenCalled();
  });
});

describe("the queue panel", () => {
  it("states why a paused queue is paused and resumes on request", async () => {
    api.getQueue.mockResolvedValue(ok(queued(["waiting"], "FAILED")));
    await mount(chat());
    expect(await within(panel()).findByText("Paused because the last reply did not finish.")).toBeTruthy();
    api.pauseQueue.mockResolvedValue(ok(queued(["waiting"])));
    fireEvent.click(within(panel()).getByRole("button", { name: "Resume queue" }));
    await waitFor(() => expect(api.pauseQueue).toHaveBeenCalledWith("c1", false));
    expect(await within(panel()).findByRole("button", { name: "Pause queue" })).toBeTruthy();
  });

  it("follows the reply a resume started", async () => {
    api.getQueue.mockResolvedValue(ok(queued(["resumed message"], "CANCELLED")));
    await mount(chat());
    api.pauseQueue.mockResolvedValue(ok(queued([], "", { runId: "r5", input: "resumed message" })));
    api.getQueue.mockResolvedValue(ok(queued([])));
    fireEvent.click(await within(panel()).findByRole("button", { name: "Resume queue" }));
    await waitFor(() => expect(FakeSource.all.some((s) => s.url.includes("/r5/"))).toBe(true));
    expect(await screen.findByText("resumed message")).toBeTruthy();
    expect(await screen.findByText("Working…")).toBeTruthy();
  });

  it("sends the next message by hand once the reply has ended, and shows it as the user's turn", async () => {
    api.getQueue.mockResolvedValue(ok(queued(["by hand"], "USER")));
    await mount(chat());
    api.sendQueuedNext.mockResolvedValue(ok({ runId: "r9", status: "PENDING" }));
    fireEvent.click(await within(panel()).findByRole("button", { name: "Send next" }));
    await waitFor(() => expect(api.sendQueuedNext).toHaveBeenCalledWith("c1"));
    await waitFor(() => expect(FakeSource.all.some((s) => s.url.includes("/r9/"))).toBe(true));
    expect(screen.getAllByText("by hand").length).toBeGreaterThan(0);
  });

  it("edits and removes a queued message through the server", async () => {
    api.getQueue.mockResolvedValue(ok(queued(["first", "second"], "USER")));
    await mount(chat());
    const second = await within(panel()).findByRole("listitem", { name: "Queued message 2 of 2" });
    fireEvent.click(within(second).getByRole("button", { name: "Edit" }));
    fireEvent.change(within(second).getByRole("textbox"), { target: { value: "second, better" } });
    api.editQueued.mockResolvedValue(ok(queued(["first", "second, better"], "USER")));
    fireEvent.click(within(second).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.editQueued).toHaveBeenCalledWith("c1", "q2", "second, better"));
    expect(await within(panel()).findByText("second, better")).toBeTruthy();
    api.removeQueued.mockResolvedValue(ok({ items: [{ id: "q2", position: 1, input: "second, better", sending: false }], paused: "USER", started: null }));
    fireEvent.click(within(within(panel()).getByRole("listitem", { name: "Queued message 1 of 2" })).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(api.removeQueued).toHaveBeenCalledWith("c1", "q1"));
    expect(await within(panel()).findByRole("listitem", { name: "Queued message 1 of 1" })).toBeTruthy();
  });

  it("names every pause reason in words, and an unknown one through the error vocabulary", () => {
    for (const r of ["USER", "FAILED", "TIMED_OUT", "CANCELLED", "DISCONNECTED", "INTERRUPTED", "BUDGET_REACHED"]) {
      expect(describePause(r)).toMatch(/^Paused/);
    }
    expect(describePause("")).toBe("");
  });
});

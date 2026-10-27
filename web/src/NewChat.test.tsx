// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
const newChatButton = () => screen.getAllByRole("button", { name: "New chat" })[0];
const sidebarItems = () => Array.from(document.querySelectorAll("nav.side li button.item")).map((b) => b.getAttribute("aria-current") ?? "-");

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

describe("New chat", () => {
  it("creates a real chat, lists it first, selects it and clears the screen", async () => {
    await mount([chat("c1", { title: "Old talk" })]);
    api.getMessages.mockResolvedValue(page(["hello", "hi there"]));
    fireEvent.click(screen.getByText("Old talk"));
    await screen.findByText("hi there");

    api.createChat.mockResolvedValue(ok(chat("c2")));
    api.getMessages.mockResolvedValue(page());
    fireEvent.click(newChatButton());

    await waitFor(() => expect(sidebarItems()).toEqual(["true", "-"]));
    expect(api.createChat).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("hi there")).toBeNull();
    expect(screen.getByText("New conversation. Write a message below.")).toBeTruthy();
    expect(document.activeElement?.id).toBe("composer-input");
  });

  it("pressing it again on a blank chat does not pile up empty chats", async () => {
    await mount([]);
    api.createChat.mockResolvedValue(ok(chat("c2")));
    fireEvent.click(newChatButton());
    await waitFor(() => expect(sidebarItems()).toEqual(["true"]));
    fireEvent.click(newChatButton());
    fireEvent.click(newChatButton());
    expect(api.createChat).toHaveBeenCalledTimes(1);
    expect(sidebarItems()).toEqual(["true"]);
  });

  it("a double click while the request is in flight creates one chat", async () => {
    await mount([]);
    let release!: (v: unknown) => void;
    api.createChat.mockReturnValue(new Promise((r) => (release = r)));
    fireEvent.click(newChatButton());
    fireEvent.click(newChatButton());
    release(ok(chat("c2")));
    await waitFor(() => expect(sidebarItems()).toEqual(["true"]));
    expect(api.createChat).toHaveBeenCalledTimes(1);
  });

  it("shows why when POST /api/chats fails, and leaves the current chat alone", async () => {
    await mount([chat("c1", { title: "Old talk" })]);
    api.getMessages.mockResolvedValue(page(["hello", "hi there"]));
    fireEvent.click(screen.getByText("Old talk"));
    await screen.findByText("hi there");

    api.createChat.mockResolvedValue({ ok: false, code: "UPSTREAM_UNAVAILABLE" });
    fireEvent.click(newChatButton());
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toBe("");
    expect(sidebarItems()).toEqual(["true"]);
    expect(screen.getByText("hi there")).toBeTruthy();

    // and it can be tried again
    api.createChat.mockResolvedValue(ok(chat("c2")));
    fireEvent.click(newChatButton());
    await waitFor(() => expect(sidebarItems()).toEqual(["true", "-"]));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("mid-run: the run is neither lost nor duplicated, and reopening the chat re-attaches to it", async () => {
    await mount([chat("c1", { title: "Busy", activeRunId: "r1" })]);
    await waitFor(() => expect(FakeSource.all.length).toBe(1));
    expect(FakeSource.all[0].url).toContain("r1");

    api.createChat.mockResolvedValue(ok(chat("c2")));
    fireEvent.click(newChatButton());
    await waitFor(() => expect(sidebarItems()).toEqual(["true", "-"]));
    expect(FakeSource.all[0].closed).toBe(true);
    expect(api.sendMessage).not.toHaveBeenCalled();
    expect(api.cancelRun).not.toHaveBeenCalled();
    expect(FakeSource.all.length).toBe(1); // nothing re-sent, nothing duplicated

    fireEvent.click(screen.getByText("Busy"));
    await waitFor(() => expect(FakeSource.all.length).toBe(2));
    expect(FakeSource.all[1].url).toContain("r1");
  });

  it("the first message goes into the new chat once, and a failed send retries with the same key", async () => {
    await mount([]);
    api.createChat.mockResolvedValue(ok(chat("c2")));
    fireEvent.click(newChatButton());
    await waitFor(() => expect(sidebarItems()).toEqual(["true"]));

    api.sendMessage.mockResolvedValueOnce({ ok: false, code: "UPSTREAM_UNAVAILABLE" });
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "first" } });
    fireEvent.submit(document.querySelector("form.composer")!);
    await screen.findByText("Send again");
    expect(api.createChat).toHaveBeenCalledTimes(1);
    const firstKey = api.sendMessage.mock.calls[0][2];

    api.sendMessage.mockResolvedValueOnce(ok({ runId: "r9", status: "PENDING" }));
    fireEvent.click(screen.getByText("Send again"));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(2));
    expect(api.sendMessage.mock.calls[1]).toEqual(["c2", "first", firstKey]);
    expect(api.createChat).toHaveBeenCalledTimes(1);
  });
});


describe("SSE refusals", () => {
  it.each(["RUNNING", "PENDING"])("keeps a %s run active when EventSource is refused", async (status) => {
    api.getRun.mockResolvedValue(ok({ runId: "r1", status }));
    await mount([chat("c1", { title: "Busy", activeRunId: "r1" })]);
    await waitFor(() => expect(FakeSource.all).toHaveLength(1));
    const source = FakeSource.all[0];
    source.readyState = FakeSource.CLOSED;
    act(() => source.onerror?.());
    expect(await screen.findByText("Live updates disconnected. The run is still active. Reconnect to resume updates.")).toBeTruthy();
    expect(api.getRun).toHaveBeenCalledWith("r1");
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    expect(api.cancelRun).not.toHaveBeenCalled();
    expect(api.sendMessage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Reconnect" }));
    await waitFor(() => expect(FakeSource.all).toHaveLength(2));
    expect(FakeSource.all[1].url).toContain("r1");
  });

  it("still reports a confirmed run failure after a stream refusal", async () => {
    api.getRun.mockResolvedValue(ok({ runId: "r1", status: "FAILED" }));
    await mount([chat("c1", { title: "Busy", activeRunId: "r1" })]);
    await waitFor(() => expect(FakeSource.all).toHaveLength(1));
    FakeSource.all[0].readyState = FakeSource.CLOSED;
    act(() => FakeSource.all[0].onerror?.());
    expect(await screen.findByText("This response did not finish: the assistant reported an error.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Reconnect" })).toBeNull();
  });
});

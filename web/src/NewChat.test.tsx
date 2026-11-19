// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";

const api = vi.hoisted(() => ({
  listChats: vi.fn(),
  createChat: vi.fn(),
  changeChat: vi.fn(),
  deleteChat: vi.fn(),
  getMessages: vi.fn(),
  sendMessage: vi.fn(),
  getRun: vi.fn(),
  cancelRun: vi.fn(),
}));
const drafts = vi.hoisted(() => ({ listOwnTemplates: vi.fn() }));

vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));
vi.mock("./draftApi", async (orig) => ({ ...(await orig<typeof import("./draftApi")>()), ...drafts }));

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
  await waitFor(() => expect(screen.queryByText("Loadingâ€¦")).toBeNull());
};
const newChatButton = () => screen.getAllByRole("button", { name: "New chat" })[0];
const sidebarItems = () => Array.from(document.querySelectorAll("nav.side li a.item")).map((b) => b.getAttribute("aria-current") ?? "-");

beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal("EventSource", FakeSource);
  api.getMessages.mockResolvedValue(page());
  api.getRun.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
  drafts.listOwnTemplates.mockResolvedValue([]);
});
afterEach(() => {
  FakeSource.all = [];
  Object.values(api).forEach((m) => m.mockReset());
  drafts.listOwnTemplates.mockReset();
});

describe("New chat", () => {
  it("opens an empty conversation and writes no chat row until the first message is sent", async () => {
    await mount([chat("c1", { title: "Old talk" })]);
    api.getMessages.mockResolvedValue(page(["hello", "hi there"]));
    fireEvent.click(screen.getByRole("link", { name: "Old talk" }));
    await screen.findByText("hi there");

    api.createChat.mockResolvedValue(ok(chat("c2")));
    api.sendMessage.mockResolvedValue(ok({ runId: "r9", status: "PENDING" }));
    api.getMessages.mockResolvedValue(page());
    fireEvent.click(newChatButton());

    expect(api.createChat).not.toHaveBeenCalled();
    expect(sidebarItems()).toEqual(["-"]); // the old chat is still there, nothing new is listed or selected
    expect(screen.queryByText("hi there")).toBeNull();
    expect(screen.getByText("Start a conversation below.")).toBeTruthy();
    expect(document.activeElement?.id).toBe("composer-input");

    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "first" } });
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(sidebarItems()).toEqual(["page", "-"]));
    expect(api.createChat).toHaveBeenCalledTimes(1);
  });

  it("the Visualize choice on the empty screen is saved into the chat its first message creates (#198)", async () => {
    await mount([]);
    api.createChat.mockResolvedValue(ok(chat("c2")));
    api.changeChat.mockResolvedValue(ok(chat("c2", { visualize: true })));
    api.sendMessage.mockResolvedValue(ok({ runId: "r9", status: "PENDING" }));
    fireEvent.click(newChatButton());
    const box = (await screen.findByRole("checkbox", { name: /Visualize/ })) as HTMLInputElement;
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    expect(box.checked).toBe(true);
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "draw it" } });
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.changeChat).toHaveBeenCalledWith("c2", { visualize: true }));
    expect(api.sendMessage).toHaveBeenCalled();
  });

  it("pressing it again on the empty screen creates nothing", async () => {
    await mount([]);
    fireEvent.click(newChatButton());
    fireEvent.click(newChatButton());
    fireEvent.click(newChatButton());
    expect(api.createChat).not.toHaveBeenCalled();
    expect(sidebarItems()).toEqual([]);
  });

  it("shows why when creating the chat fails on send, and keeps the text in the box", async () => {
    await mount([chat("c1", { title: "Old talk" })]);
    fireEvent.click(newChatButton());

    api.createChat.mockResolvedValue({ ok: false, code: "UPSTREAM_UNAVAILABLE" });
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "first" } });
    fireEvent.submit(document.querySelector("form.composer")!);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toBe("");
    expect(sidebarItems()).toEqual(["-"]);
    expect((document.getElementById("composer-input") as HTMLTextAreaElement).value).toBe("first");

    // and it can be tried again
    api.createChat.mockResolvedValue(ok(chat("c2")));
    api.sendMessage.mockResolvedValue(ok({ runId: "r9", status: "PENDING" }));
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(sidebarItems()).toEqual(["page", "-"]));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("mid-run: the run is neither lost nor duplicated, and reopening the chat re-attaches to it", async () => {
    await mount([chat("c1", { title: "Busy", activeRunId: "r1" })]);
    await waitFor(() => expect(FakeSource.all.length).toBe(1));
    expect(FakeSource.all[0].url).toContain("r1");

    fireEvent.click(newChatButton());
    expect(sidebarItems()).toEqual(["-"]);
    expect(FakeSource.all[0].closed).toBe(true);
    expect(api.createChat).not.toHaveBeenCalled();
    expect(api.sendMessage).not.toHaveBeenCalled();
    expect(api.cancelRun).not.toHaveBeenCalled();
    expect(FakeSource.all.length).toBe(1); // nothing re-sent, nothing duplicated

    fireEvent.click(screen.getByRole("link", { name: "Busy" }));
    await waitFor(() => expect(FakeSource.all.length).toBe(2));
    expect(FakeSource.all[1].url).toContain("r1");
  });

  it("a first message the server refuses leaves no chat row, and the text goes back into the box", async () => {
    await mount([chat("c1", { title: "Old talk" })]);
    api.createChat.mockResolvedValue(ok(chat("c2")));
    api.deleteChat.mockResolvedValue(ok(true));
    fireEvent.click(newChatButton());

    api.sendMessage.mockResolvedValueOnce({ ok: false, code: "UPSTREAM_UNAVAILABLE" });
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "first" } });
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(api.deleteChat).toHaveBeenCalledWith("c2"));
    await waitFor(() => expect(sidebarItems()).toEqual(["-"]));
    expect(screen.getByText("Start a conversation below.")).toBeTruthy();
    expect((document.getElementById("composer-input") as HTMLTextAreaElement).value).toBe("first");

    // and it can be sent again
    api.createChat.mockResolvedValue(ok(chat("c3")));
    api.sendMessage.mockResolvedValue(ok({ runId: "r9", status: "PENDING" }));
    fireEvent.submit(document.querySelector("form.composer")!);
    await waitFor(() => expect(sidebarItems()).toEqual(["page", "-"]));
  });

  it("offers the saved templates as starters on the new-chat screen", async () => {
    drafts.listOwnTemplates.mockResolvedValue([{ id: "t1", name: "summarise", body: "Summarise the text below." }]);
    await mount([]);
    fireEvent.click(await screen.findByRole("button", { name: "summarise" }));
    expect((document.getElementById("composer-input") as HTMLTextAreaElement).value).toBe("Summarise the text below.");
  });

  it("shows no starters when the user has saved none", async () => {
    await mount([]);
    expect(screen.queryByRole("list", { name: "Your templates" })).toBeNull();
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

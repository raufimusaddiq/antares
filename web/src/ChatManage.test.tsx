// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ChatSummary, Inventory } from "./chatApi";
import { loadDraft, saveDraft } from "./drafts";

// Chat management in the sidebar (issue #148, UXP-ACC-018): the controls are reachable by keyboard and named,
// destructive ones ask first, and nothing leaves the list until the server has confirmed it.

const api = vi.hoisted(() => ({
  listChats: vi.fn(),
  changeChat: vi.fn(),
  deleteChat: vi.fn(),
  getChat: vi.fn(),
  getMessages: vi.fn(),
  getRun: vi.fn(),
  sendMessage: vi.fn(),
  getCapabilities: vi.fn(),
  searchChats: vi.fn(),
}));

vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const EMAIL = "a@example.test";
const chat = (id: string, title: string, extra: Partial<ChatSummary> = {}): ChatSummary => ({
  id,
  title,
  createdAt: "",
  updatedAt: `2026-01-0${id.slice(1)}T00:00:00Z`,
  activeRunId: "",
  pinned: false,
  archived: false,
  deleteFailed: false,
  visualize: false,
  ...extra,
});
const ok = <T,>(value: T) => ({ ok: true as const, value });
const fail = (code: string, status = 409) => ({ ok: false as const, code, status });
const inventory = (deleteAvailable: boolean): Inventory => ({
  capabilities: [{ id: "chat_delete", label: "Delete conversations", available: deleteAvailable, reason: deleteAvailable ? "" : "Hermes does not advertise a session delete route" }],
  toolsets: [],
  providers: [],
  skills: [],
});
const emptyPage = ok({ turns: [], nextCursor: "", total: 0 });

class FakeSource {
  close() {}
  addEventListener() {}
}

async function mount(chats: ChatSummary[], deleteAvailable = true) {
  api.listChats.mockImplementation((_cursor?: string, archived?: boolean) => Promise.resolve(ok({ chats: archived ? [] : chats, nextCursor: "" })));
  api.getCapabilities.mockResolvedValue(ok(inventory(deleteAvailable)));
  render(<Chat email={EMAIL} onSignOut={() => {}} />);
  await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
  await waitFor(() => expect(api.getCapabilities).toHaveBeenCalled());
}

const rows = () => Array.from(document.querySelectorAll("nav.side > ul > li button.item")).map((b) => b.textContent ?? "");
const trigger = (title: string) => screen.getByRole("button", { name: `Actions for ${title}` });
const openMenu = (title: string) => {
  fireEvent.click(trigger(title));
  return screen.getByRole("menu", { name: `Actions for ${title}` });
};

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeSource);
  api.getMessages.mockResolvedValue(emptyPage);
  api.getRun.mockResolvedValue(fail("NOT_FOUND", 404));
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});

describe("the actions menu", () => {
  it("is a named button that opens a menu, moves by arrow keys and closes on Escape back onto its button", async () => {
    await mount([chat("c1", "Talk")]);
    const btn = trigger("Talk");
    expect(btn.getAttribute("aria-haspopup")).toBe("menu");
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    const menu = openMenu("Talk");
    expect(btn.getAttribute("aria-expanded")).toBe("true");
    const items = within(menu).getAllByRole("menuitem");
    expect(items.map((i) => i.textContent)).toEqual(["Rename…", "Pin to top", "Archive", "Delete…"]);
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[1]);
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement).toBe(items[3]);
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[0]); // wraps
    fireEvent.keyDown(menu, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(btn);
    expect(api.changeChat).not.toHaveBeenCalled();
    expect(api.deleteChat).not.toHaveBeenCalled();
  });

  it("is also under Conversation actions for the open chat (the phone's home for it, in the Options panel)", async () => {
    await mount([chat("c1", "Talk")]);
    await waitFor(() => expect(document.querySelector("nav.side button.item.current")).not.toBeNull());
    const slot = document.querySelector("#chat-extras .chat-actions-slot")!;
    const btn = within(slot as HTMLElement).getByRole("button", { name: "Conversation actions" });
    fireEvent.click(btn);
    const menu = screen.getByRole("menu", { name: "Actions for Talk" });
    expect(within(menu).getAllByRole("menuitem")).toHaveLength(4);
    fireEvent.keyDown(menu, { key: "Escape" });
    expect(document.activeElement).toBe(btn);
  });

  it("offers an archived chat restore, not pin", async () => {
    await mount([chat("c1", "Talk", { archived: true })]);
    const menu = openMenu("Talk");
    expect(within(menu).getAllByRole("menuitem").map((i) => i.textContent)).toEqual(["Rename…", "Restore from archive", "Delete…"]);
  });
});

describe("rename", () => {
  it("saves through the server and shows the title the server returned; focus returns to the button", async () => {
    await mount([chat("c1", "Talk")]);
    api.changeChat.mockResolvedValue(ok(chat("c1", "Roadmap")));
    fireEvent.click(within(openMenu("Talk")).getByRole("menuitem", { name: "Rename…" }));
    const dlg = screen.getByRole("dialog", { name: "Rename conversation" });
    expect(dlg.getAttribute("aria-modal")).toBe("true");
    const input = within(dlg).getByLabelText("Name") as HTMLInputElement;
    expect(input.value).toBe("Talk");
    expect(document.activeElement).toBe(input);
    fireEvent.change(input, { target: { value: "  Roadmap  " } });
    fireEvent.click(within(dlg).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(api.changeChat).toHaveBeenCalledWith("c1", { title: "Roadmap" });
    expect(rows()).toEqual(["Roadmap"]);
    expect(document.activeElement).toBe(trigger("Roadmap"));
  });

  it("keeps the dialog and the old title when the server refuses, and says so", async () => {
    await mount([chat("c1", "Talk")]);
    api.changeChat.mockResolvedValue(fail("HERMES_UNAVAILABLE", 503));
    fireEvent.click(within(openMenu("Talk")).getByRole("menuitem", { name: "Rename…" }));
    const dlg = screen.getByRole("dialog");
    fireEvent.change(within(dlg).getByLabelText("Name"), { target: { value: "Other" } });
    fireEvent.click(within(dlg).getByRole("button", { name: "Save" }));
    expect((await within(dlg).findByRole("alert")).textContent).toMatch(/unavailable/i);
    expect(rows()).toEqual(["Talk"]);
  });

  it("Escape and Cancel change nothing; a blank name cannot be saved", async () => {
    await mount([chat("c1", "Talk")]);
    fireEvent.click(within(openMenu("Talk")).getByRole("menuitem", { name: "Rename…" }));
    const dlg = screen.getByRole("dialog");
    fireEvent.change(within(dlg).getByLabelText("Name"), { target: { value: "   " } });
    expect((within(dlg).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(dlg, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.changeChat).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(trigger("Talk"));
  });

  it("keeps Tab inside the dialog", async () => {
    await mount([chat("c1", "Talk")]);
    fireEvent.click(within(openMenu("Talk")).getByRole("menuitem", { name: "Rename…" }));
    const dlg = screen.getByRole("dialog");
    const save = within(dlg).getByRole("button", { name: "Save" });
    save.focus();
    fireEvent.keyDown(dlg, { key: "Tab" });
    expect(document.activeElement).toBe(within(dlg).getByLabelText("Name"));
    fireEvent.keyDown(dlg, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(save);
  });
});

describe("pin and archive", () => {
  it("pins through the server and lists in the order the server returns", async () => {
    await mount([chat("c1", "One"), chat("c2", "Two")]);
    api.changeChat.mockResolvedValue(ok(chat("c2", "Two", { pinned: true })));
    api.listChats.mockImplementation(() => Promise.resolve(ok({ chats: [chat("c2", "Two", { pinned: true }), chat("c1", "One")], nextCursor: "" })));
    fireEvent.click(within(openMenu("Two")).getByRole("menuitem", { name: "Pin to top" }));
    await waitFor(() => expect(rows()[0]).toMatch(/^Two\s*Pinned$/));
    expect(api.changeChat).toHaveBeenCalledWith("c2", { pinned: true });
    expect(document.querySelector('[role="status"]')?.textContent).toBe("Pinned Two.");
  });

  it("archives out of the main list and shows it under 'Show archived', from where it can be restored", async () => {
    await mount([chat("c1", "One"), chat("c2", "Two")]);
    api.changeChat.mockResolvedValue(ok(chat("c2", "Two", { archived: true })));
    fireEvent.click(within(openMenu("Two")).getByRole("menuitem", { name: "Archive" }));
    await waitFor(() => expect(rows()).toEqual(["One"]));
    expect(api.changeChat).toHaveBeenCalledWith("c2", { archived: true });

    api.listChats.mockImplementation((_c?: string, archived?: boolean) => Promise.resolve(ok({ chats: archived ? [chat("c2", "Two", { archived: true })] : [chat("c1", "One")], nextCursor: "" })));
    fireEvent.click(screen.getByRole("button", { name: "Show archived" }));
    const archive = await screen.findByRole("region", { name: "Archived conversations" });
    await within(archive).findByText("Two");
    expect(api.listChats).toHaveBeenCalledWith(undefined, true);

    api.changeChat.mockResolvedValue(ok(chat("c2", "Two")));
    api.listChats.mockImplementation((_c?: string, archived?: boolean) => Promise.resolve(ok({ chats: archived ? [] : [chat("c2", "Two"), chat("c1", "One")], nextCursor: "" })));
    fireEvent.click(within(archive).getByRole("button", { name: "Actions for Two" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Restore from archive" }));
    await waitFor(() => expect(rows()).toEqual(["Two", "One"]));
    expect(api.changeChat).toHaveBeenLastCalledWith("c2", { archived: false });
  });

  it("does not move a chat when the server refuses, and says why", async () => {
    await mount([chat("c1", "One")]);
    api.changeChat.mockResolvedValue(fail("CHAT_STATE"));
    fireEvent.click(within(openMenu("One")).getByRole("menuitem", { name: "Archive" }));
    await waitFor(() => expect(document.querySelector('[role="status"]')?.textContent).toMatch(/can't be changed/));
    expect(rows()).toEqual(["One"]);
  });
});

describe("delete", () => {
  it("asks first with Cancel focused, and Cancel deletes nothing", async () => {
    await mount([chat("c1", "Secret plans")]);
    fireEvent.click(within(openMenu("Secret plans")).getByRole("menuitem", { name: "Delete…" }));
    const dlg = screen.getByRole("alertdialog", { name: "Delete “Secret plans”?" });
    expect(dlg.getAttribute("aria-describedby")).toBeTruthy();
    expect(document.getElementById(dlg.getAttribute("aria-describedby")!)?.textContent).toMatch(/cannot be undone/);
    expect(document.activeElement).toBe(within(dlg).getByRole("button", { name: "Cancel" }));
    fireEvent.click(within(dlg).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(api.deleteChat).not.toHaveBeenCalled();
    expect(rows()).toEqual(["Secret plans"]);
    expect(document.activeElement).toBe(trigger("Secret plans"));
  });

  it("removes the chat only after the server confirms, and clears its local draft, search and the open conversation", async () => {
    await mount([chat("c2", "Newer"), chat("c1", "Older")]);
    saveDraft(EMAIL, "c2", "unsent words");
    saveDraft(EMAIL, "c1", "other draft");
    await waitFor(() => expect(document.querySelector("nav.side button.item.current")?.textContent).toBe("Newer")); // opens the most recent
    let release!: (v: unknown) => void;
    api.deleteChat.mockReturnValue(new Promise((r) => (release = r)));
    fireEvent.click(within(openMenu("Newer")).getByRole("menuitem", { name: "Delete…" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    // In flight: still listed, the dialog says it is working, nothing was removed yet.
    expect(rows()).toEqual(["Newer", "Older"]);
    expect((screen.getByRole("button", { name: "Deleting…" }) as HTMLButtonElement).disabled).toBe(true);
    expect(loadDraft(EMAIL, "c2")).toBe("unsent words");

    release(ok({ deleted: true }));
    await waitFor(() => expect(rows()).toEqual(["Older"]));
    expect(api.deleteChat).toHaveBeenCalledWith("c2");
    expect(loadDraft(EMAIL, "c2")).toBe("");
    expect(loadDraft(EMAIL, "c1")).toBe("other draft");
    expect(screen.queryByRole("alertdialog")).toBeNull();
    // The open chat was the deleted one: the conversation on screen is now the remaining one.
    expect(document.querySelector("nav.side button.item.current")?.textContent).toBe("Older");
    expect(document.querySelector('[role="status"]')?.textContent).toBe("Deleted Newer.");
  });

  it("keeps the chat, stays open and offers a retry when the delete fails", async () => {
    await mount([chat("c1", "Keep me")]);
    api.deleteChat.mockResolvedValue(fail("HERMES_UNAVAILABLE", 503));
    fireEvent.click(within(openMenu("Keep me")).getByRole("menuitem", { name: "Delete…" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const alert = await within(screen.getByRole("alertdialog")).findByRole("alert");
    expect(alert.textContent).toMatch(/did not confirm the delete/);
    expect(alert.textContent).toMatch(/still here/);
    expect(rows()).toEqual(["Keep me"]);
    // The list shows the server's flag after a failed delete.
    api.listChats.mockImplementation(() => Promise.resolve(ok({ chats: [chat("c1", "Keep me", { deleteFailed: true })], nextCursor: "" })));
    api.deleteChat.mockResolvedValue(ok({ deleted: true }));
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(rows()).toEqual([]));
    expect(api.deleteChat).toHaveBeenCalledTimes(2);
  });

  it("an overlapping delete (409 CHAT_STATE) is not 'gone': the chat and its draft stay and the dialog says why", async () => {
    await mount([chat("c1", "Contested")]);
    saveDraft(EMAIL, "c1", "keep this");
    api.deleteChat.mockResolvedValue(fail("CHAT_STATE"));
    fireEvent.click(within(openMenu("Contested")).getByRole("menuitem", { name: "Delete…" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const alert = await within(screen.getByRole("alertdialog")).findByRole("alert");
    expect(alert.textContent).toMatch(/already being deleted/);
    expect(rows()).toEqual(["Contested"]);
    expect(loadDraft(EMAIL, "c1")).toBe("keep this");
  });

  it("marks a chat whose delete failed, in the list", async () => {
    await mount([chat("c1", "Stuck", { deleteFailed: true })]);
    expect(rows()[0]).toMatch(/Delete failed/);
    expect(within(openMenu("Stuck")).getByRole("menuitem", { name: "Retry delete…" })).toBeTruthy();
  });

  it("is a disabled menu item with the assistant's own reason when the assistant cannot delete", async () => {
    await mount([chat("c1", "Talk")], false);
    const menu = openMenu("Talk");
    const item = within(menu).getByRole("menuitem", { name: "Delete…" });
    expect(item.getAttribute("aria-disabled")).toBe("true");
    const reason = document.getElementById(item.getAttribute("aria-describedby")!);
    expect(reason?.textContent).toBe("Hermes does not advertise a session delete route");
    fireEvent.click(item);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(api.deleteChat).not.toHaveBeenCalled();
  });

  it("is disabled while the assistant's abilities are unknown, and while a reply is in progress", async () => {
    api.listChats.mockResolvedValue(ok({ chats: [chat("c1", "Busy", { activeRunId: "r1" })], nextCursor: "" }));
    api.getCapabilities.mockResolvedValue(fail("HERMES_UNAVAILABLE", 503));
    api.getRun.mockResolvedValue(ok({ runId: "r1", status: "RUNNING" }));
    render(<Chat email={EMAIL} onSignOut={() => {}} />);
    await screen.findByRole("button", { name: "Actions for Busy" });
    await waitFor(() => expect(api.getCapabilities).toHaveBeenCalled());
    const item = within(openMenu("Busy")).getByRole("menuitem", { name: "Delete…" });
    expect(item.getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(item);
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("treats the server's NOT_FOUND as already deleted", async () => {
    await mount([chat("c1", "Gone"), chat("c2", "Here")]);
    api.deleteChat.mockResolvedValue(fail("NOT_FOUND", 404));
    fireEvent.click(within(openMenu("Gone")).getByRole("menuitem", { name: "Delete…" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(rows()).toEqual(["Here"]));
  });
});

describe("a message into an archived chat", () => {
  it("brings the chat back into the list without a reload", async () => {
    api.getCapabilities.mockResolvedValue(ok(inventory(true)));
    const archivedChat = chat("c2", "Old talk", { archived: true });
    let back = false;
    api.listChats.mockImplementation((_c?: string, archived?: boolean) =>
      Promise.resolve(ok({ chats: archived ? (back ? [] : [archivedChat]) : back ? [chat("c2", "Old talk"), chat("c1", "Live")] : [chat("c1", "Live")], nextCursor: "" })),
    );
    api.sendMessage.mockImplementation(() => {
      back = true; // the server made it active when it admitted the message
      return Promise.resolve(ok({ runId: "r1", status: "PENDING" }));
    });
    render(<Chat email={EMAIL} onSignOut={() => {}} />);
    await waitFor(() => expect(rows()).toEqual(["Live"]));
    fireEvent.click(screen.getByRole("button", { name: "Show archived" }));
    const archive = await screen.findByRole("region", { name: "Archived conversations" });
    fireEvent.click(await within(archive).findByText("Old talk"));
    fireEvent.change(document.getElementById("composer-input")!, { target: { value: "hello again" } });
    fireEvent.click(screen.getByRole("button", { name: /^Send/ }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    await waitFor(() => expect(rows()).toEqual(["Old talk", "Live"]));
    await waitFor(() => expect(within(screen.getByRole("region", { name: "Archived conversations" })).queryByText("Old talk")).toBeNull());
  });
});

// #198 review: a chat opened from a search result outside the loaded list shows its own Visualize state.
describe("Visualize for a chat opened from search", () => {
  it("shows the toggle for an out-of-page result, as the server stored it", async () => {
    await mount([chat("c1", "One")]);
    api.searchChats.mockResolvedValue(
      ok({ results: [{ chatId: "c9", chatTitle: "Far away", kind: "title", turn: null, role: "title", createdAt: "", snippet: [] }], nextCursor: "", index: { complete: true, chats: 2, pending: 0, failed: 0, partial: 0 } }),
    );
    api.getChat.mockResolvedValue(ok(chat("c9", "Far away", { visualize: true })));
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "far away" } });
    fireEvent.submit(screen.getByRole("search"));
    fireEvent.click(await screen.findByRole("button", { name: /Far away/ }));
    const box = (await screen.findByRole("checkbox", { name: /Visualize/ })) as HTMLInputElement;
    expect(box.checked).toBe(true);
    expect(api.getChat).toHaveBeenCalledWith("c9");
  });
});

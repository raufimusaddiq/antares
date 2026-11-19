// @vitest-environment jsdom
// T-2: a chat, a Settings section and each screen have their own address and tab title, so they can be bookmarked,
// reloaded into, opened in a new tab and reached with Back/Forward.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getChat: vi.fn(), getMessages: vi.fn(), getRun: vi.fn(), createChat: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";
import { AdminShell } from "./Admin";
import { BlogApp } from "./blog/BlogApp";

const A = "11111111111111111111111111111111";
const B = "22222222222222222222222222222222";
const C = "33333333333333333333333333333333";
const chat = (id: string, title: string, updatedAt = ""): ChatSummary => ({ id, title, createdAt: "", updatedAt, activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false });
const ok = <T,>(value: T) => ({ ok: true as const, value });
const page = (texts: string[]) => ok({ turns: texts.map((text, seq) => ({ seq, role: seq % 2 ? "assistant" : "user", text })), nextCursor: "", total: texts.length });

const mount = async () => {
  render(<Chat email="a@example.test" onSignOut={() => {}} />);
  await screen.findByText("from two");
};

beforeEach(() => {
  window.history.replaceState(null, "", "/");
  api.listChats.mockResolvedValue(ok({ chats: [chat(A, "One", "2026-01-01"), chat(B, "Two", "2026-02-02")], nextCursor: "" }));
  api.getMessages.mockImplementation((id: string) => Promise.resolve(page(id === A ? ["from one"] : ["from two"])));
  api.getRun.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
  vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200 })));
});
afterEach(() => {
  vi.clearAllMocks();
});

describe("the address follows the open chat", () => {
  it("puts a chat at /chat/<id>, and Back returns to the chat before it", async () => {
    await mount();
    fireEvent.click(screen.getByRole("link", { name: "One" }));
    await screen.findByText("from one");
    expect(window.location.pathname).toBe(`/chat/${A}`);

    fireEvent.click(screen.getByRole("link", { name: "Two" }));
    await screen.findByText("from two");
    expect(window.location.pathname).toBe(`/chat/${B}`);

    window.history.back();
    await waitFor(() => expect(window.location.pathname).toBe(`/chat/${A}`));
    await screen.findByText("from one");
  });

  it("returns to / for a new chat", async () => {
    await mount();
    fireEvent.click(screen.getByRole("link", { name: "One" }));
    await screen.findByText("from one");
    api.createChat.mockResolvedValue(ok(chat(C, "")));
    fireEvent.click(screen.getAllByRole("button", { name: "New chat" })[0]);
    await waitFor(() => expect(window.location.pathname).toBe("/"));
  });

  it("makes each chat row a real link, and keeps a plain click in the app", async () => {
    await mount();
    const link = screen.getByRole("link", { name: "One" });
    expect(link.getAttribute("href")).toBe(`/chat/${A}`);
    expect(fireEvent.click(link)).toBe(false); // the browser's own navigation is prevented
    await screen.findByText("from one");
    expect(fireEvent.click(link, { ctrlKey: true })).toBe(true); // a new tab is the browser's to open
  });
});

describe("Settings is a place in the address", () => {
  it("opens at ?settings=<section>, follows the section, and closing returns to the chat's address", async () => {
    await mount();
    const depth = window.history.length;
    fireEvent.click(screen.getByRole("button", { name: "Menu" }));
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    await waitFor(() => expect(window.location.search).toBe("?settings=general"));

    fireEvent.click(screen.getByRole("button", { name: "Usage" }));
    await waitFor(() => expect(window.location.search).toBe("?settings=usage"));

    fireEvent.click(screen.getByRole("button", { name: "Close settings" }));
    await waitFor(() => expect(window.location.search).toBe(""));
    expect(window.location.pathname).toBe("/");
    expect(window.history.length).toBe(depth); // an overlay, not a page: no entry left behind
  });

  it("opens the section a link names", async () => {
    window.history.replaceState(null, "", `/chat/${A}?settings=usage`);
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Usage" }).getAttribute("aria-current")).toBe("page"));
  });

  it("shows the first section when the link names one this account is not offered", async () => {
    window.history.replaceState(null, "", `/chat/${A}?settings=jobs`);
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await screen.findByText("from two");
    expect(screen.getByRole("button", { name: "General" }).getAttribute("aria-current")).toBe("page");
    expect(screen.queryByRole("button", { name: "Scheduled tasks" })).toBeNull();
  });
});

describe("the tab names the view", () => {
  it("names the open chat, and a new chat", async () => {
    await mount();
    expect(document.title).toBe("Two · Antares");
    api.createChat.mockResolvedValue(ok(chat(C, "")));
    fireEvent.click(screen.getAllByRole("button", { name: "New chat" })[0]);
    await waitFor(() => expect(document.title).toBe("New chat · Antares"));
  });

  it("names an admin screen and a blog screen", () => {
    const { unmount } = render(
      <AdminShell email="a@example.test" route={{ kind: "users" }} onNavigate={() => {}} onSignOut={() => {}}>
        <p>users</p>
      </AdminShell>,
    );
    expect(document.title).toBe("Users · Antares");
    unmount();
    render(<BlogApp caps={{ writing: false, check: false, webSearch: false }} email="a@example.test" route={{ kind: "list" }} onNavigate={() => {}} onChats={() => {}} />);
    expect(document.title).toBe("My posts · Antares");
  });
});

// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getChat: vi.fn(), getMessages: vi.fn(), getRun: vi.fn(), createChat: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { App } from "./App";

const ID = "33333333333333333333333333333333";
const ok = <T,>(value: T) => ({ ok: true as const, value });
const chat = (id: string, title: string): ChatSummary => ({ id, title, createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false });

beforeEach(() => {
  api.listChats.mockResolvedValue(ok({ chats: [], nextCursor: "" }));
  api.getChat.mockResolvedValue({ ok: false, code: "NOT_FOUND", status: 404 });
  api.getMessages.mockResolvedValue(ok({ turns: [], nextCursor: "", total: 0 }));
  api.createChat.mockResolvedValue({ ok: false, code: "HERMES_UNAVAILABLE" });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      url.includes("/api/blog/capabilities")
        ? Response.json({ writing: true, check: false, webSearch: false })
        : Response.json({ email: "a@example.test", state: "ACTIVE", canAdmin: false, displayName: "", requestedAt: "" }),
    ),
  );
});
afterEach(() => {
  sessionStorage.clear();
  vi.clearAllMocks();
});

describe("a chat link that survived sign-in", () => {
  it("is read from the sign-in round trip's key, cleared, and handed to Chat", async () => {
    sessionStorage.setItem("antares.deeplink", ID);
    window.history.replaceState(null, "", "/");
    render(<App />);
    await waitFor(() => expect(api.getChat).toHaveBeenCalledWith(ID));
    expect(sessionStorage.getItem("antares.deeplink")).toBeNull();
  });

  it("ignores a stored value that is not a chat id", async () => {
    sessionStorage.setItem("antares.deeplink", "../admin");
    window.history.replaceState(null, "", "/");
    render(<App />);
    await waitFor(() => expect(api.listChats).toHaveBeenCalled());
    expect(api.getChat).not.toHaveBeenCalled();
  });

  it("opens the chat the address names, so a reload stays on it", async () => {
    window.history.replaceState(null, "", `/chat/${ID}`);
    render(<App />);
    await waitFor(() => expect(api.getChat).toHaveBeenCalledWith(ID));
  });

  it("follows the address, not the link it opened at first, when Chat remounts", async () => {
    const OLD = "11111111111111111111111111111111";
    const OTHER = "22222222222222222222222222222222";
    api.listChats.mockResolvedValue(ok({ chats: [chat(OTHER, "Two")], nextCursor: "" }));
    api.getChat.mockResolvedValue(ok(chat(OLD, "One")));
    api.createChat.mockResolvedValue(ok(chat("44444444444444444444444444444444", "")));

    window.history.replaceState(null, "", `/chat/${OLD}`);
    render(<App />);
    await waitFor(() => expect(document.title).toBe("One · Antares"));

    fireEvent.click(screen.getAllByRole("button", { name: "New chat" })[0]);
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    fireEvent.click((await screen.findAllByRole("button", { name: "Blog posts" }))[0]);
    await waitFor(() => expect(window.location.pathname).toBe("/blog"));
    fireEvent.click(await screen.findByRole("button", { name: "Chats" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));

    // "/" means the newest chat; the link opened when the app loaded is spent.
    await waitFor(() => expect(document.title).toBe("Two · Antares"));
    expect(api.getMessages.mock.calls.filter(([id]) => id === OLD)).toHaveLength(1);
  });
});

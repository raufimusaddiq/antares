// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";

const api = vi.hoisted(() => ({
  listChats: vi.fn(),
  getChat: vi.fn(),
  getMessages: vi.fn(),
  getRun: vi.fn(),
}));

vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const MINE = "11111111111111111111111111111111";
const THEIRS = "22222222222222222222222222222222";
const chat = (id: string): ChatSummary => ({ id, title: "", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false });
const ok = <T,>(value: T) => ({ ok: true as const, value });

beforeEach(() => {
  api.listChats.mockResolvedValue(ok({ chats: [chat(MINE)], nextCursor: "" }));
  api.getMessages.mockResolvedValue(ok({ turns: [], nextCursor: "", total: 0 }));
  window.history.replaceState(null, "", `/chat/${THEIRS}`);
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("a notification link", () => {
  it("opens the linked chat when the server says it is the reader's", async () => {
    api.getChat.mockResolvedValue(ok(chat(THEIRS)));
    render(<Chat email="a@example.test" onSignOut={() => {}} initialChatId={THEIRS} />);
    await waitFor(() => expect(api.getMessages).toHaveBeenCalledWith(THEIRS));
    expect(window.location.pathname).toBe("/");
  });

  it("opens nothing of someone else's chat: the server says not found, the reader is told, and no history is requested for it", async () => {
    api.getChat.mockResolvedValue({ ok: false, code: "NOT_FOUND", status: 404 });
    render(<Chat email="a@example.test" onSignOut={() => {}} initialChatId={THEIRS} />);
    await screen.findByText(/not available to this account/);
    expect(api.getChat).toHaveBeenCalledWith(THEIRS);
    await waitFor(() => expect(api.getMessages).toHaveBeenCalledWith(MINE));
    expect(api.getMessages.mock.calls.map((c) => c[0])).not.toContain(THEIRS);
    expect(document.querySelector('nav.side [aria-current="true"]')).not.toBeNull();
    expect(window.location.pathname).toBe("/");
  });
});

// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getMessages: vi.fn(), sendMessage: vi.fn(), getCapabilities: vi.fn(), getQueue: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const ok = <T,>(value: T) => ({ ok: true as const, value });
const chat: ChatSummary = { id: "c1", title: "Talk", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false };
const box = () => screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;

beforeEach(() => {
  api.listChats.mockResolvedValue(ok({ chats: [chat], nextCursor: "" }));
  api.getCapabilities.mockResolvedValue(
    ok({ capabilities: [{ id: "regenerate", label: "", available: false, reason: "no retry route" }, { id: "edit_turn", label: "", available: false, reason: "no edit route" }], toolsets: [], providers: [], skills: [] }),
  );
  api.getQueue.mockResolvedValue(ok({ items: [], paused: "", started: null }));
  api.getMessages.mockResolvedValue(
    ok({
      turns: [
        { seq: 0, role: "user", text: "first question" },
        { seq: 1, role: "assistant", text: "first answer" },
        { seq: 2, role: "user", text: "what about Tuesday?" },
        { seq: 3, role: "assistant", text: "Tuesday is free." },
      ],
      nextCursor: "",
      total: 4,
    }),
  );
});
afterEach(() => {
  cleanup();
  Object.values(api).forEach((m) => m.mockReset());
});

describe("edit and regenerate have a working alternative (#166)", () => {
  it("Ask again writes the question the newest answer replied to into the composer, and sends nothing", async () => {
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    fireEvent.click(await screen.findByRole("button", { name: "Ask again" }));
    expect(box().value).toBe("what about Tuesday?");
    expect(document.activeElement).toBe(box());
    expect(api.sendMessage).not.toHaveBeenCalled();
  });

  it("Edit as new message keeps what is already typed and adds the earlier message after it", async () => {
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await screen.findByRole("button", { name: "Edit as new message" });
    fireEvent.change(box(), { target: { value: "note to self" } });
    fireEvent.click(screen.getByRole("button", { name: "Edit as new message" }));
    await waitFor(() => expect(box().value).toBe("note to self\n\nwhat about Tuesday?"));
    expect(api.sendMessage).not.toHaveBeenCalled();
  });
});

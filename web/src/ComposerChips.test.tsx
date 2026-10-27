// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getMessages: vi.fn(), sendMessage: vi.fn(), getCapabilities: vi.fn(), getChatModel: vi.fn(), getUsage: vi.fn(), putChatModel: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const ok = <T,>(value: T) => ({ ok: true as const, value });
const usage = { tokens: { input: 900, output: 100, cacheRead: 0, cacheWrite: 0, total: 1000 }, finishedRuns: 1, reportedRuns: 1, costIDR: null, unpricedRuns: 0, unknownModelRuns: 0, turns: [], turnsTruncated: false, priceListDate: "", windowTokens: 128000, carriedTokens: null, budget: { limit: null, used: null, enforced: false, reached: false, basis: "" } };

beforeEach(() => {
  api.listChats.mockResolvedValue(ok({ chats: [{ id: "c1", title: "Talk", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false }], nextCursor: "" }));
  api.getMessages.mockResolvedValue(ok({ turns: [], nextCursor: "", total: 0 }));
  api.getCapabilities.mockResolvedValue(ok({ capabilities: [{ id: "models", label: "", available: true, reason: "" }], toolsets: [], providers: [], skills: [] }));
  api.getChatModel.mockResolvedValue(ok({ choice: { provider: "custom", model: "m-reason", reasoningEffort: "low", serviceTier: "" } }));
  api.getUsage.mockResolvedValue(ok(usage));
});
afterEach(() => {
  cleanup();
  Object.values(api).forEach((m) => m.mockReset());
});

describe("model and usage beside the composer (#164)", () => {
  it("shows the chat's model and effort next to the input, outside the Options drawer, and usage at a glance", async () => {
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    fireEvent.click(await screen.findByRole("button", { name: "Talk" }));
    const chip = await screen.findByText("Model: m-reason · low effort");
    const row = chip.closest(".composer-chips")!;
    expect(row).toBeTruthy();
    expect(document.getElementById("chat-extras")!.contains(chip)).toBe(false);
    // One open away from the list: the chip is the picker itself (open it, then pick).
    expect(chip.tagName).toBe("SUMMARY");
    const glance = await screen.findByRole("button", { name: "1,000 tokens used · context in use unknown" });
    expect(row.contains(glance)).toBe(true);
    fireEvent.click(glance);
    expect(document.getElementById("chat-extras")!.classList.contains("open")).toBe(true);
    expect(api.sendMessage).not.toHaveBeenCalled();
  });
});

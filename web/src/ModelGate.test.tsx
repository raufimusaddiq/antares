// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary, Inventory } from "./chatApi";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getMessages: vi.fn(), getRun: vi.fn(), getCapabilities: vi.fn(), getUsage: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const ok = <T,>(value: T) => ({ ok: true as const, value });
const chat = (id: string): ChatSummary => ({ id, title: "t", createdAt: "", updatedAt: "", activeRunId: "" });
const inv = (status: boolean): Inventory => ({ capabilities: [{ id: "models", label: "Model choices", available: status, reason: status ? "" : "none" }], toolsets: [], providers: [], skills: [] });

beforeEach(() => {
  api.listChats.mockResolvedValue(ok({ chats: [chat("c1")], nextCursor: "" }));
  api.getMessages.mockResolvedValue(ok({ turns: [], nextCursor: "", total: 0 }));
  api.getRun.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
  api.getUsage.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ id: "c1", model: null }), { status: 200 })));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});

const mount = async () => {
  render(<Chat email="a@example.test" onSignOut={() => {}} />);
  await waitFor(() => expect(api.getCapabilities).toHaveBeenCalled());
};

describe("model picker is gated on discovery", () => {
  it("is offered when discovery says models are available", async () => {
    api.getCapabilities.mockResolvedValue(ok(inv(true)));
    await mount();
    expect(await screen.findByText(/^Model/)).toBeTruthy();
  });
  it.each([
    ["unsupported", ok(inv(false))],
    ["unreadable", { ok: false, code: "HERMES_UNAVAILABLE" }],
  ])("is not offered when discovery is %s", async (_n, answer) => {
    api.getCapabilities.mockResolvedValue(answer);
    await mount();
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(/^Model/)).toBeNull();
  });
});

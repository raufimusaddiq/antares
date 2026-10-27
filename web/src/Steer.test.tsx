// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ChatSummary, Inventory } from "./chatApi";

const api = vi.hoisted(() => ({
  listChats: vi.fn(),
  getMessages: vi.fn(),
  getRun: vi.fn(),
  sendMessage: vi.fn(),
  steerRun: vi.fn(),
  getCapabilities: vi.fn(),
}));

vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const ok = <T,>(value: T) => ({ ok: true as const, value });
const fail = (code: string, status: number) => ({ ok: false as const, code, status });
const chat = (id: string, extra: Partial<ChatSummary> = {}): ChatSummary => ({ id, title: "Busy", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, ...extra });
const inventory = (steer: boolean): Inventory => ({
  capabilities: [{ id: "steer", label: "Steer a running reply", available: steer, reason: steer ? "" : "Hermes does not report run_steer as enabled" }],
  toolsets: [],
  providers: [],
  skills: [],
});

class FakeSource {
  static CLOSED = 2;
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  addEventListener() {}
  close() {
    this.readyState = 2;
  }
}

const box = () => screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;

// A chat whose reply is still running, opened with the given steer capability.
const mountLive = async (steer: boolean) => {
  api.listChats.mockResolvedValue(ok({ chats: [chat("c1", { activeRunId: "r1" })], nextCursor: "" }));
  api.getCapabilities.mockResolvedValue(ok(inventory(steer)));
  render(<Chat email="a@example.test" onSignOut={() => {}} />);
  await waitFor(() => expect(api.getCapabilities).toHaveBeenCalled());
  await screen.findByText("Working…");
  fireEvent.change(box(), { target: { value: "use the second file" } });
};

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeSource);
  api.getMessages.mockResolvedValue(ok({ turns: [{ seq: 0, role: "user", text: "pick a file" }], nextCursor: "", total: 1 }));
  api.getRun.mockResolvedValue(ok({ runId: "r1", status: "RUNNING" }));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});

describe("steer during a live reply (#149)", () => {
  it("keeps the composer editable, sends guidance to the run, and adds no user turn", async () => {
    api.steerRun.mockResolvedValue(ok(true));
    await mountLive(true);
    expect((screen.getByRole("button", { name: "Send" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(await screen.findByRole("button", { name: "Steer" }));
    expect(api.steerRun).toHaveBeenCalledWith("r1", "use the second file");
    expect(await screen.findByText(/Guidance accepted/)).toBeTruthy();
    expect(box().value).toBe("");
    expect(api.sendMessage).not.toHaveBeenCalled();
    expect(screen.queryByText("use the second file")).toBeNull(); // no ghost turn
  });

  it("calls a refusal from a finished run too late and keeps the text", async () => {
    api.steerRun.mockResolvedValue(fail("STEER_REFUSED", 409));
    await mountLive(true);
    api.getRun.mockResolvedValue(ok({ runId: "r1", status: "COMPLETED" }));
    fireEvent.click(await screen.findByRole("button", { name: "Steer" }));
    expect(await screen.findByText(/Too late/)).toBeTruthy();
    expect(box().value).toBe("use the second file");
  });

  it("calls a refusal from a live run a refusal, not too late, and keeps the text", async () => {
    api.steerRun.mockResolvedValue(fail("STEER_REFUSED", 409));
    await mountLive(true);
    fireEvent.click(await screen.findByRole("button", { name: "Steer" }));
    expect(await screen.findByText(/did not accept guidance/)).toBeTruthy();
    expect(screen.queryByText(/Too late/)).toBeNull();
    expect(box().value).toBe("use the second file");
  });

  it("shows another failure in plain words and keeps the text", async () => {
    api.steerRun.mockResolvedValue(fail("HERMES_UNAVAILABLE", 503));
    await mountLive(true);
    fireEvent.click(await screen.findByRole("button", { name: "Steer" }));
    expect(await screen.findByText("The assistant is unavailable. Try again shortly.")).toBeTruthy();
    expect(box().value).toBe("use the second file");
  });

  it("is not offered when Hermes does not support steering", async () => {
    await mountLive(false);
    expect(screen.queryByRole("button", { name: "Steer" })).toBeNull();
    expect(api.steerRun).not.toHaveBeenCalled();
  });
});

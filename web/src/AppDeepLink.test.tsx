// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getChat: vi.fn(), getMessages: vi.fn(), getRun: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { App } from "./App";

const ID = "33333333333333333333333333333333";
const ok = <T,>(value: T) => ({ ok: true as const, value });

beforeEach(() => {
  api.listChats.mockResolvedValue(ok({ chats: [], nextCursor: "" }));
  api.getChat.mockResolvedValue({ ok: false, code: "NOT_FOUND", status: 404 });
  api.getMessages.mockResolvedValue(ok({ turns: [], nextCursor: "", total: 0 }));
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ email: "a@example.test", state: "ACTIVE", canAdmin: false, displayName: "", requestedAt: "" }), { status: 200 })),
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
});

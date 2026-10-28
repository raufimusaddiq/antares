// @vitest-environment jsdom
// Behaviour of the phone-layout toggles (#138). jsdom applies no CSS, so this proves the state, ARIA and keyboard
// contract only; the layout itself is checked in a real browser by scripts/browser-check.mjs (ONLY=mobile-layout).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getMessages: vi.fn(), getRun: vi.fn(), getUsage: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

const ok = <T,>(value: T) => ({ ok: true as const, value });

beforeEach(() => {
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
  api.listChats.mockResolvedValue(ok({ chats: [], nextCursor: "" }));
  api.getMessages.mockResolvedValue(ok({ turns: [], nextCursor: "", total: 0 }));
  api.getRun.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
  api.getUsage.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});

const mount = async (props: { canAdmin?: boolean } = {}) => {
  render(<Chat email="a@example.test" onSignOut={() => {}} onAdmin={() => {}} {...props} />);
  await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
};

describe("phone layout toggles", () => {
  it("keeps the account actions inside the menu the toggle controls, and Escape closes it", async () => {
    await mount({ canAdmin: true });
    const menu = screen.getByRole("button", { name: "Menu" });
    const panel = document.getElementById(menu.getAttribute("aria-controls") ?? "");
    expect(menu.getAttribute("aria-expanded")).toBe("false");
    expect(panel?.classList.contains("open")).toBe(false);
    for (const name of ["Sign out", "Admin"]) expect(panel?.contains(screen.getByRole("button", { name }))).toBe(true);
    fireEvent.click(menu);
    expect(menu.getAttribute("aria-expanded")).toBe("true");
    expect(panel?.classList.contains("open")).toBe(true);
    fireEvent.keyDown(screen.getByRole("button", { name: "Sign out" }), { key: "Escape" });
    expect(menu.getAttribute("aria-expanded")).toBe("false");
  });

  it("closes the menu on a press outside the header", async () => {
    await mount();
    const menu = screen.getByRole("button", { name: "Menu" });
    fireEvent.click(menu);
    fireEvent.pointerDown(menu);
    expect(menu.getAttribute("aria-expanded")).toBe("true"); // inside the header: untouched
    fireEvent.pointerDown(document.body);
    expect(menu.getAttribute("aria-expanded")).toBe("false");
  });

  it("folds the search box behind a toggle and keeps the form in the document", async () => {
    await mount();
    const toggle = screen.getByRole("button", { name: "Search" });
    const body = document.getElementById(toggle.getAttribute("aria-controls") ?? "");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(body?.contains(screen.getByRole("searchbox"))).toBe(true);
    fireEvent.click(toggle);
    expect(screen.getByRole("button", { name: "Close search" }).getAttribute("aria-expanded")).toBe("true");
    expect(document.querySelector(".search")?.classList.contains("open")).toBe(true);
  });

  it("puts the per-chat options behind a toggle", async () => {
    await mount();
    const toggle = screen.getByRole("button", { name: "Options" });
    const drawer = document.getElementById(toggle.getAttribute("aria-controls") ?? "");
    expect(drawer?.classList.contains("open")).toBe(false);
    expect(drawer?.textContent).toContain("My templates");
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(drawer?.classList.contains("open")).toBe(true);
  });

  it("opens the options once when the token limit is reached, and the toggle can close and reopen them", async () => {
    const usage = { tokens: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, total: 2 }, finishedRuns: 1, reportedRuns: 1, costIDR: null, unpricedRuns: 0, unknownModelRuns: 0, turns: [], turnsTruncated: false, priceListDate: "", windowTokens: null, carriedTokens: null, spending: null, budget: { limit: 10000, used: 20000, enforced: true, reached: true, basis: "" } };
    api.listChats.mockResolvedValue(ok({ chats: [{ id: "c1", title: "Talk", createdAt: "", updatedAt: "", activeRunId: "" }], nextCursor: "" }));
    api.getUsage.mockResolvedValue(ok(usage));
    await mount();
    fireEvent.click(await screen.findByRole("button", { name: "Talk" }));
    const drawer = () => document.getElementById("chat-extras");
    await waitFor(() => expect(drawer()?.classList.contains("open")).toBe(true));
    const toggle = screen.getByRole("button", { name: /^Options/ });
    expect(toggle.getAttribute("aria-expanded")).toBe("true"); // what is shown and what is announced agree
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(drawer()?.classList.contains("open")).toBe(false); // the log can grow back
    expect(toggle.textContent).toContain("token limit reached"); // the closed toggle still says why
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(drawer()?.classList.contains("open")).toBe(true);
  });
});

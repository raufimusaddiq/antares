// @vitest-environment jsdom
// The markup the stylesheet's grid relies on (#139): the header holds the brand and one account group (identity, Menu toggle,
// actions popover), the starting card is real markup, and decorative parts are hidden from assistive technology.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";

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

describe("shell markup", () => {
  it("puts the brand and one account group in the header, and the conversation list and pane beside it", async () => {
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
    const app = document.querySelector(".chat-app")!;
    expect([...app.children].map((c) => c.tagName + "." + c.className.split(" ")[0])).toEqual(["HEADER.bar", "NAV.side", "MAIN.pane"]);
    const bar = app.querySelector("header.bar")!;
    expect([...bar.children].map((c) => c.className.split(" ")[0])).toEqual(["brand", "account"]);
    const account = bar.querySelector(".account")!;
    expect(account.querySelector(".who-am-i")?.textContent).toBe("a@example.test");
    const menu = screen.getByRole("button", { name: "Menu" });
    expect(account.contains(menu)).toBe(true);
    expect(account.contains(document.getElementById(menu.getAttribute("aria-controls") ?? ""))).toBe(true);
  });

  it("shows the starting card as real markup, with the decorative tag hidden from assistive technology", async () => {
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await screen.findByText("Start a conversation below.");
    const pane = document.querySelector("main.pane")!;
    expect(pane.classList.contains("empty")).toBe(true);
    const hero = pane.querySelector(".hero")!;
    expect(hero.querySelector(".hero-tag")?.getAttribute("aria-hidden")).toBe("true");
    expect(hero.querySelector("p.hero-text")?.textContent).toBe("Start a conversation below.");
  });

  it("keeps every icon decorative: the buttons are named by their text", async () => {
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
    for (const name of ["New chat", "Search", "Menu", "Sign out"]) expect(screen.getByRole("button", { name })).toBeTruthy();
    for (const el of document.querySelectorAll("[data-icon]")) expect((el.textContent ?? "").trim().length, el.outerHTML.slice(0, 80)).toBeGreaterThan(0);
  });
});

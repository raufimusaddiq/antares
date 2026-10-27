// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { APPEARANCE_KEY, applyAppearance, DEFAULT_APPEARANCE, loadAppearance, saveAppearance } from "./appearance";
import { AppearancePanel, DiagnosticsPanel } from "./Settings";

const root = document.documentElement;
const openPanel = (c: HTMLElement) => {
  const d = c.querySelector("details")!;
  d.open = true;
  fireEvent(d, new Event("toggle"));
};

beforeEach(() => {
  localStorage.clear();
  for (const a of ["data-theme", "data-text", "data-focus", "data-sidebar"]) root.removeAttribute(a);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// #113: appearance is per device, survives a reload, and refuses values it does not know.
describe("appearance", () => {
  it("defaults when nothing or garbage is stored, and keeps only known values", () => {
    expect(APPEARANCE_KEY).toBe("antares:appearance:v1"); // the documented key (docs/runbooks/identity.md)
    expect(loadAppearance()).toEqual(DEFAULT_APPEARANCE);
    localStorage.setItem(APPEARANCE_KEY, "{not json");
    expect(loadAppearance()).toEqual(DEFAULT_APPEARANCE);
    localStorage.setItem(APPEARANCE_KEY, JSON.stringify({ theme: "neon", text: "huge", focus: "yes", sidebar: "wide" }));
    expect(loadAppearance()).toEqual({ ...DEFAULT_APPEARANCE, sidebar: "wide" });
  });

  it("a choice is saved, read back after a reload, and applied to the page", () => {
    const chosen = { theme: "dark", text: "larger", focus: true, sidebar: "narrow" } as const;
    saveAppearance(chosen);
    expect(loadAppearance()).toEqual(chosen);
    applyAppearance(loadAppearance());
    expect([root.dataset.theme, root.dataset.text, root.hasAttribute("data-focus"), root.dataset.sidebar]).toEqual(["dark", "larger", true, "narrow"]);
    applyAppearance(DEFAULT_APPEARANCE); // the defaults leave no attribute behind
    expect(["data-theme", "data-text", "data-focus", "data-sidebar"].some((a) => root.hasAttribute(a))).toBe(false);
  });

  it("the panel applies and saves a change at once", () => {
    render(<AppearancePanel />);
    fireEvent.change(screen.getByLabelText("Theme"), { target: { value: "light" } });
    fireEvent.click(screen.getByRole("checkbox"));
    expect(root.dataset.theme).toBe("light");
    expect(root.hasAttribute("data-focus")).toBe(true);
    expect(loadAppearance()).toMatchObject({ theme: "light", focus: true });
  });
});

// #113: diagnostics read only public, secret-free endpoints and show nothing that identifies the session.
describe("diagnostics", () => {
  it("shows the build and the connection, and nothing from the cookie or the account", async () => {
    document.cookie = "visible_cookie=COOKIE-VALUE-123";
    const asked: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        asked.push(path);
        const body = path === "/api/version" ? `{"revision":"0123456789abcdef"}` : path === "/source-hash.txt" ? "fedcba9876543210\n" : "ok";
        return new Response(body, { status: 200 });
      }),
    );
    const { container } = render(<DiagnosticsPanel />);
    expect(asked).toEqual([]); // nothing is fetched until the panel is opened
    openPanel(container);
    await waitFor(() => expect(container.textContent).toContain("0123456789ab"));
    const text = container.textContent ?? "";
    expect(text).toContain("fedcba987654");
    expect(text).toContain("ready");
    expect(text).not.toContain("COOKIE-VALUE-123");
    expect(text).not.toMatch(/@|token|key|secret/i);
    expect([...new Set(asked)].sort()).toEqual(["/api/version", "/readyz", "/source-hash.txt"]);
  });

  it("says unknown or unreachable instead of guessing", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("offline"))));
    const { container } = render(<DiagnosticsPanel />);
    openPanel(container);
    await waitFor(() => expect(container.textContent).toContain("unreachable"));
    expect(container.textContent).toContain("unknown");
  });
});

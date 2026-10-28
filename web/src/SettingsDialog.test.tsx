// @vitest-environment jsdom
// #139 stage 3: the Settings dialog holds every account and device panel as a section. Only the open section is
// mounted, so it loads when shown; a panel shown as a page has no disclosure to open.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Panel } from "./Panel";
import { SettingsDialog } from "./SettingsDialog";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

describe("Panel", () => {
  it("is a disclosure in a menu and a plain section as a page", () => {
    const toggled = vi.fn();
    const { container, rerender } = render(<Panel page={false} className="memory-panel" title="Memory" onToggle={toggled}><p>body</p></Panel>);
    expect(container.querySelector("details > summary")?.textContent).toBe("Memory");
    rerender(<Panel page className="memory-panel" title="Memory"><p>body</p></Panel>);
    expect(container.querySelector("details")).toBeNull();
    expect(screen.getByRole("region", { name: "Memory" }).textContent).toBe("body");
  });
});

describe("SettingsDialog", () => {
  it("opens on its section, mounts only that section, and loads a section when it is shown", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (path: string) => {
      calls.push(path);
      if (path === "/api/capabilities") return json({ capabilities: [], toolsets: [], providers: [], skills: [] });
      return json({ text: "", max: 2000 });
    }));
    const onSignOutEverywhere = vi.fn();
    render(<SettingsDialog tab="general" jobs={false} onClose={() => {}} onSignOutEverywhere={onSignOutEverywhere} />);
    expect(screen.getByRole("button", { name: "General" }).getAttribute("aria-current")).toBe("page");
    expect(screen.queryByRole("button", { name: "Scheduled tasks" })).toBeNull(); // not offered
    expect(screen.getByRole("region", { name: "Appearance" })).toBeTruthy();
    expect(calls.some((c) => c.includes("personality"))).toBe(false); // the Memory page is not mounted yet
    fireEvent.click(screen.getByRole("button", { name: "Sign out everywhere" }));
    expect(onSignOutEverywhere).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole("button", { name: "Memory & skills" }));
    expect(screen.getByRole("button", { name: "Memory & skills" }).getAttribute("aria-current")).toBe("page");
    expect(screen.queryByRole("region", { name: "Appearance" })).toBeNull();
    await screen.findByRole("region", { name: "Memory & Skills" });
    await vi.waitFor(() => expect(calls).toContain("/api/me/personality"));
  });

  it("offers Scheduled tasks only when the account has them, and closes from its button", () => {
    const onClose = vi.fn();
    render(<SettingsDialog tab="jobs" jobs onClose={onClose} onSignOutEverywhere={() => {}} />);
    expect(screen.getByRole("button", { name: "Scheduled tasks" }).getAttribute("aria-current")).toBe("page");
    fireEvent.click(screen.getByRole("button", { name: "Close settings" }));
    expect(onClose).toHaveBeenCalledOnce();
  });
});

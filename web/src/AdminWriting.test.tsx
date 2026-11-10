// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WritingRulesCard } from "./AdminWriting";
import { parseWritingRules, type WritingRules } from "./adminWritingApi";

afterEach(cleanup);

const rules = (over: Partial<WritingRules> = {}): WritingRules => ({
  enabled: true,
  version: "v1",
  source: "anti-slop (github.com/miqdadbadjuber/anti-slop)",
  license: "MIT, Copyright (c) 2026 Miqdad Badjuber (antislop)",
  last24h: { with: 148, without: 0, notApplied: 0 },
  warning: "",
  ...over,
});

describe("the Writing rules card", () => {
  it("shows state, version, credit and the counts, and says what they cover", () => {
    render(<WritingRulesCard rules={rules()} onChanged={() => {}} />);
    expect(screen.getByRole("switch").getAttribute("aria-checked") ?? (screen.getByRole("switch") as HTMLInputElement).checked).toBeTruthy();
    expect(screen.getByText("Writing rules are on for everyone")).toBeTruthy();
    expect(screen.getByText("writing-rules v1")).toBeTruthy();
    expect(screen.getByText(/Miqdad Badjuber \(antislop\)/)).toBeTruthy();
    expect(screen.getByText("148")).toBeTruthy();
    expect(screen.getByText(/chat and post-check runs/)).toBeTruthy();
    expect(screen.getByText(/Titles and summaries are not counted/)).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows off, with the without count labelled", () => {
    render(<WritingRulesCard rules={rules({ enabled: false, last24h: { with: 63, without: 85, notApplied: 0 } })} onChanged={() => {}} />);
    expect(screen.getByText("Writing rules are off for everyone")).toBeTruthy();
    expect(screen.getByText("without (switched off)")).toBeTruthy();
  });

  it("warns only when the server says some runs were not covered", () => {
    const { rerender } = render(<WritingRulesCard rules={rules({ last24h: { with: 145, without: 3, notApplied: 3 } })} onChanged={() => {}} />);
    expect(screen.queryByRole("alert")).toBeNull();
    rerender(<WritingRulesCard rules={rules({ warning: "not_applied", last24h: { with: 145, without: 3, notApplied: 3 } })} onChanged={() => {}} />);
    expect(screen.getByRole("alert").textContent).toContain("The rules were not applied to 3 runs since this feature shipped.");
  });

  it("sends the opposite of what is shown, keeps focus on the switch while saving, and reports the saved state", async () => {
    let release: (v: { ok: true; value: WritingRules }) => void = () => {};
    const save = vi.fn(() => new Promise<{ ok: true; value: WritingRules }>((r) => (release = r)));
    const changed = vi.fn();
    render(<WritingRulesCard rules={rules()} onChanged={changed} save={save} />);
    const sw = screen.getByRole("switch") as HTMLInputElement;
    sw.focus();
    fireEvent.click(sw);
    expect(save).toHaveBeenCalledWith(false);
    expect(sw.getAttribute("aria-disabled")).toBe("true");
    expect(sw.disabled).toBe(false);
    expect(document.activeElement).toBe(sw);
    fireEvent.click(sw); // a second press while saving does nothing
    expect(save).toHaveBeenCalledTimes(1);
    release({ ok: true, value: rules({ enabled: false }) });
    await waitFor(() => expect(changed).toHaveBeenCalledWith(rules({ enabled: false })));
  });

  it("shows an error and claims nothing was saved", async () => {
    const save = vi.fn(async () => ({ ok: false as const, code: "INTERNAL", status: 500 }));
    const changed = vi.fn();
    render(<WritingRulesCard rules={rules()} onChanged={changed} save={save} />);
    fireEvent.click(screen.getByRole("switch"));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe("Could not save. Nothing was changed."));
    expect(changed).not.toHaveBeenCalled();
  });
});

describe("parseWritingRules", () => {
  const ok = { enabled: true, version: "v1", source: "s", license: "l", last24h: { with: 1, without: 2, notApplied: 0 } };
  it("reads a good answer", () => {
    expect(parseWritingRules({ ...ok, warning: "not_applied" })?.warning).toBe("not_applied");
    expect(parseWritingRules(ok)?.warning).toBe("");
  });
  it("refuses what it cannot understand", () => {
    for (const bad of [null, [], {}, { ...ok, enabled: "yes" }, { ...ok, last24h: { with: -1, without: 0, notApplied: 0 } }, { ...ok, last24h: { with: 1 } }, { ...ok, version: 1 }]) {
      expect(parseWritingRules(bad)).toBeNull();
    }
  });
});

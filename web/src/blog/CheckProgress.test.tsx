// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CheckProgressView, durationWords } from "./CheckProgress";
import type { Check, CheckProgress } from "./types";

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const T0 = Date.parse("2026-11-11T10:00:00Z");
const iso = (ms: number) => new Date(T0 + ms).toISOString();
const check = (p: Partial<CheckProgress> = {}, over: Partial<Check> = {}): Check => ({
  id: "c1",
  state: "running",
  reason: "",
  notes: [],
  notices: [],
  blocks: [],
  progress: { stage: "reading", stageSince: iso(0), lastEventAt: iso(0), lookups: 0, startedAt: iso(0), now: iso(0), ...p },
  ...over,
});
const mount = (c: Check | null, extra: { onCancel?: () => void } = {}) => render(<CheckProgressView check={c} onCancel={extra.onCancel ?? (() => {})} cancelDisabled={false} />);

describe("check progress", () => {
  it("names the step: reading is current, later steps wait, and no percentage is ever drawn", () => {
    vi.setSystemTime(T0);
    mount(check());
    const cur = document.querySelector(".step-current");
    expect(cur?.textContent).toContain("Reading your post");
    expect(document.querySelectorAll(".step-pending")).toHaveLength(2);
    expect(document.body.textContent).not.toMatch(/\d+\s?%/);
    expect(document.querySelector("progress, [role=progressbar]")).toBeNull();
  });

  it("looking things up is current with its count; reading is done", () => {
    vi.setSystemTime(T0 + 20000);
    mount(check({ stage: "searching", lookups: 3, now: iso(20000) }));
    expect(document.querySelector(".step-current")?.textContent).toContain("Looking things up · 3 lookups");
    expect(document.querySelectorAll(".step-done")).toHaveLength(1);
    expect(document.querySelector(".step-done")?.textContent).toContain("Reading your post");
  });

  it("writing: lookups done; with no lookups it says none were needed", () => {
    vi.setSystemTime(T0 + 30000);
    mount(check({ stage: "writing", lookups: 0, now: iso(30000) }));
    expect(document.querySelector(".step-current")?.textContent).toContain("Writing suggestions");
    expect(document.body.textContent).toContain("none needed");
    expect(document.querySelectorAll(".step-done")).toHaveLength(2);
  });

  it("the clock ticks from the server's time, and is silent for assistive technology", () => {
    vi.setSystemTime(T0 + 5000);
    mount(check({ now: iso(5000), lastEventAt: iso(3000) }));
    expect(screen.getByText(/5 s so far/)).toBeTruthy();
    act(() => vi.advanceTimersByTime(3000));
    expect(screen.getByText(/8 s so far/)).toBeTruthy();
    const clock = screen.getByText(/so far/).closest("[aria-hidden]");
    expect(clock?.getAttribute("aria-hidden")).toBe("true");
    expect(screen.getByText(/last sign of life 5 s ago/i)).toBeTruthy();
  });

  it("an unreliable browser clock does not matter: elapsed comes from the server's now", () => {
    vi.setSystemTime(T0 - 3600_000); // the browser's clock is an hour wrong
    mount(check({ now: iso(65000) }));
    expect(screen.getByText(/1 min 5 s so far/)).toBeTruthy();
  });

  it("the live region changes only when the step changes, politely", () => {
    vi.setSystemTime(T0);
    const { rerender } = mount(check());
    const live = () => document.querySelector("[aria-live=polite]")?.textContent;
    expect(live()).toBe("Reading your post");
    act(() => vi.advanceTimersByTime(10000));
    expect(live()).toBe("Reading your post");
    rerender(<CheckProgressView check={check({ stage: "searching", lookups: 1, now: iso(10000) })} onCancel={() => {}} cancelDisabled={false} />);
    expect(live()).toBe("Looking things up");
  });

  it("nothing new for 45 s says it is still working", () => {
    vi.setSystemTime(T0 + 50000);
    mount(check({ stage: "searching", lookups: 1, lastEventAt: iso(1000), now: iso(50000) }));
    expect(screen.getByText(/Still working\. Nothing new for 49 s\./)).toBeTruthy();
    expect(document.querySelector("[aria-live=polite]")?.textContent).toContain("Nothing new for a while");
  });

  it("without progress yet (just starting) it shows reading at zero", () => {
    vi.setSystemTime(T0);
    mount(null);
    expect(document.querySelector(".step-current")?.textContent).toContain("Reading your post");
    expect(screen.getByText(/0 s so far/)).toBeTruthy();
  });

  it("Cancel is a named button", () => {
    vi.setSystemTime(T0);
    const onCancel = vi.fn();
    mount(check(), { onCancel });
    screen.getByRole("button", { name: "Cancel" }).click();
    expect(onCancel).toHaveBeenCalled();
  });
});

describe("durations", () => {
  it("reads as seconds, then minutes and seconds", () => {
    expect(durationWords(0)).toBe("0 s");
    expect(durationWords(45_000)).toBe("45 s");
    expect(durationWords(72_000)).toBe("1 min 12 s");
    expect(durationWords(180_000)).toBe("3 min 0 s");
    expect(durationWords(-5000)).toBe("0 s");
  });
});

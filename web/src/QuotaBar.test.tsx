// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QuotaBar, SharedQuotaMeter, grouped, resetsIn } from "./QuotaBar";
import { parseSharedQuota } from "./chatApi";

afterEach(cleanup);

describe("shared quota bars", () => {
  it("groups whole numbers the Indonesian way", () => {
    expect(grouped(6000000)).toBe("6.000.000");
    expect(grouped(6381221631)).toBe("6.381.221.631");
    expect(grouped(516)).toBe("516");
  });

  it("is a meter with the share used and when it resets, as text as well as a bar", () => {
    const now = Date.parse("2026-10-29T05:00:00Z");
    expect(resetsIn("2026-11-03T05:08:00Z", now)).toBe("in 5 days");
    expect(resetsIn("2026-10-29T08:00:00Z", now)).toBe("in 3 hours");
    expect(resetsIn("2026-10-29T05:20:00Z", now)).toBe("in 20 minutes");
    expect(resetsIn("2026-10-29T04:00:00Z", now)).toBe("soon");
    render(<QuotaBar label="Week" frac={0.365} resetsAt="2099-01-01T00:00:00Z" />);
    const m = screen.getByRole("meter", { name: "Week quota" });
    expect(m.getAttribute("aria-valuenow")).toBe("36.5");
    expect(m.getAttribute("aria-valuetext")).toMatch(/^36,5% used, resets in \d+ days$/);
    expect(document.body.textContent).toContain("36,5% used");
    expect(document.querySelector(".quota-row")!.classList.contains("nearly")).toBe(false);
  });

  it("says a nearly spent window is nearly used up, in words and not only by colour", () => {
    render(<QuotaBar label="Month" frac={0.92} resetsAt="2099-01-01T00:00:00Z" />);
    expect(document.body.textContent).toContain("92% used, nearly used up");
    expect(document.querySelector(".quota-row")!.classList.contains("nearly")).toBe(true);
  });

  it("shows the users' meter only when the server has plan figures to share", () => {
    const { container, rerender } = render(<SharedQuotaMeter quota={{ state: "NOT_CONFIGURED", plan: "", windows: [] }} className="quota-side" />);
    expect(container.innerHTML).toBe("");
    rerender(<SharedQuotaMeter quota={{ state: "OK", plan: "Studio", windows: [{ key: "week", usedFrac: 0.365, resetsAt: "2099-01-01T00:00:00Z" }, { key: "month", usedFrac: 0.589, resetsAt: "2099-01-01T00:00:00Z" }] }} className="quota-side" />);
    expect(screen.getByRole("region", { name: "Shared usage limit" })).toBeTruthy();
    expect(screen.getAllByRole("meter").map((m) => m.getAttribute("aria-label"))).toEqual(["Week quota", "Month quota"]);
    expect(document.body.textContent).toContain("Shared limit, all users");
    expect(document.body.textContent).not.toMatch(/Rp/);
  });

  it("parses only a well-formed answer", () => {
    expect(parseSharedQuota({ state: "OK", plan: "Studio", windows: [{ key: "week", usedFrac: 0.5, resetsAt: "x" }] })).toEqual({ state: "OK", plan: "Studio", windows: [{ key: "week", usedFrac: 0.5, resetsAt: "x" }] });
    expect(parseSharedQuota({ state: "NONE" })).toEqual({ state: "NONE", plan: "", windows: [] });
    for (const bad of [null, {}, { state: "MAYBE" }, { state: "OK", windows: [{ key: "week", usedFrac: "half" }] }]) expect(parseSharedQuota(bad), JSON.stringify(bad)).toBeNull();
  });
});

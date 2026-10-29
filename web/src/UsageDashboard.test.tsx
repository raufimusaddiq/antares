// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { tokens, UsageDashboard } from "./UsageDashboard";


const t = (o: Partial<Record<string, number | null>> = {}) => ({ runs: 0, unreported: 0, unpriced: 0, input: 0, output: 0, cacheRead: 0, estimate: null, ...o });
const range = (days: number) => ({
  days,
  since: "2026-10-21",
  currency: "IDR",
  priceDate: "2026-10-23",
  total: t({ runs: 3, unreported: 1, unpriced: 1, input: 1000, output: 2000, estimate: 5500 }),
  byModel: [{ model: "deepseek-v4-1-flash", ...t({ runs: 1, output: 1000, estimate: 5500 }) }, { model: "other", ...t({ runs: 1, unpriced: 1, output: 1000 }) }],
  byChat: [{ chatId: "", title: "", deleted: true, ...t({ runs: 1, output: 500 }) }],
  byDay: Array.from({ length: days }, (_, i) => ({ day: `2026-10-${String(21 + (i % 7)).padStart(2, "0")}`, ...t() })),
});
const open = (c: HTMLElement) => {
  const d = c.querySelector("details")!;
  d.open = true;
  fireEvent(d, new Event("toggle"));
};

it("a group whose replies all reported nothing shows unknown tokens, never 0", () => {
  expect(tokens(t({ runs: 2, unreported: 2 }))).toBe("unknown");
  expect(tokens(t({ runs: 3, unreported: 1, input: 10, output: 5 }))).toBe("15");
  expect(tokens(t())).toBe("0"); // no replies at all: zero is the truth
});

// #151: reads only while open, switches range, keeps unknowns unknown, labels the estimate and deleted chats.
describe("usage dashboard", () => {
  it("reads nothing until opened, then shows the range honestly", async () => {
    const asked: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        asked.push(path);
        const days = Number(new URL(path, "https://x").searchParams.get("days"));
        return new Response(JSON.stringify(range(days)), { status: 200 });
      }),
    );
    const { container } = render(<UsageDashboard />);
    expect(asked).toEqual([]);
    open(container);
    await screen.findByText(/3,000 tokens/);
    expect(asked).toEqual(["/api/me/usage?days=7"]);
    const text = container.textContent ?? "";
    expect(text).toContain("1 with no usage reported");
    expect(text).toContain("1 on a model with no known price");
    expect(text).toContain("not the provider's bill");
    expect(text).toContain("no known price");
    expect(text).toContain("Deleted chats");
    expect(text).not.toMatch(/Rp 0\b/);
    expect(container.querySelectorAll(".usage-bar")).toHaveLength(7);
    // The per-day table is its own <details>: opening and closing it leaves the dashboard open.
    const inner = container.querySelectorAll("details")[1] as HTMLDetailsElement;
    for (const state of [true, false]) {
      inner.open = state;
      fireEvent(inner, new Event("toggle", { bubbles: true }));
      expect(container.querySelectorAll(".usage-bar")).toHaveLength(7);
    }
    fireEvent.click(screen.getByRole("button", { name: "30 days" }));
    await waitFor(() => expect(container.querySelectorAll(".usage-bar")).toHaveLength(30));
    expect(asked).toContain("/api/me/usage?days=30");
  });
});

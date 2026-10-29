// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { JobsPanel, when } from "./Jobs";


const open = (c: HTMLElement) => {
  const d = c.querySelector("details")!;
  d.open = true;
  fireEvent(d, new Event("toggle"));
};
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const job = { id: "0123456789ab", name: "Morning digest", prompt: "news", schedule: "0 9 * * *", state: "scheduled", paused: false, nextRunAt: null, lastRunAt: null, lastStatus: null };

// #105: the panel reads the account's own tasks only while open, says plainly when the assistant has none, and
// explains a refused schedule in words.
describe("scheduled tasks", () => {
  it("fetches nothing until opened, then lists and acts on the caller's tasks", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init?: RequestInit) => {
        calls.push(`${init?.method ?? "GET"} ${path}`);
        if (path === "/api/jobs" && (init?.method ?? "GET") === "GET") return json(200, { jobs: [job], max: 10 });
        return json(200, { ...job, paused: true });
      }),
    );
    const { container } = render(<JobsPanel />);
    expect(calls).toEqual([]);
    open(container);
    await screen.findByText("Morning digest");
    fireEvent.click(screen.getByRole("button", { name: "Pause Morning digest" }));
    await waitFor(() => expect(calls).toContain("POST /api/jobs/0123456789ab/pause"));
  });

  it("says when the assistant has no scheduled tasks", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => json(501, { error: { code: "CAPABILITY_UNAVAILABLE", message: "x" } })));
    const { container } = render(<JobsPanel />);
    open(container);
    expect((await screen.findByRole("alert")).textContent).toBe("Scheduled tasks are not available on this assistant.");
    expect(screen.queryByRole("button", { name: "Add task" })).toBeNull();
  });

  it("explains a schedule that runs too often", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_p: string, init?: RequestInit) => ((init?.method ?? "GET") === "GET" ? json(200, { jobs: [], max: 10 }) : json(400, { error: { code: "SCHEDULE_TOO_FREQUENT", message: "x" } }))),
    );
    const { container } = render(<JobsPanel />);
    open(container);
    await screen.findByText("No scheduled tasks.");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Ping" } });
    fireEvent.change(screen.getByLabelText("Schedule"), { target: { value: "every 5m" } });
    fireEvent.change(screen.getByLabelText("Instructions"), { target: { value: "ping" } });
    fireEvent.click(screen.getByRole("button", { name: "Add task" }));
    expect((await screen.findByRole("alert")).textContent).toContain("at most once an hour");
  });
});

it("shows times in UTC, as the panel says", () => {
  expect(when("2026-10-27T02:30:00+07:00")).toBe("2026-10-26 19:30 UTC");
  expect(when(null)).toBe("—");
  expect(when("not a time")).toBe("—");
});

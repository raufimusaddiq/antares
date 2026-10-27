// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemorySkillsPanel } from "./MemorySkills";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const inventory = {
  capabilities: [{ id: "skills", label: "Skills", status: "available" }],
  toolsets: [],
  providers: [],
  skills: [
    { name: "arxiv", description: "Search papers", category: "research" },
    { name: "weather", description: "Forecasts", category: "daily" },
  ],
};
const open = (c: HTMLElement) => {
  const d = c.querySelector("details")!;
  d.open = true;
  fireEvent(d, new Event("toggle"));
};

// #111: the panel reads only while open, lists and searches skills read-only, says memory editing is unavailable,
// and shows the instructions the server stored.
describe("Memory & Skills", () => {
  it("reads nothing until opened, then searches skills and saves the caller's instructions", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init?: RequestInit) => {
        calls.push(`${init?.method ?? "GET"} ${path}`);
        if (path === "/api/capabilities") return json(200, inventory);
        if (init?.method === "PUT") return json(200, { text: "Be brief.", max: 2000 });
        return json(200, { text: "", max: 2000 });
      }),
    );
    const { container } = render(<MemorySkillsPanel />);
    expect(calls).toEqual([]);
    open(container);
    await screen.findByText("arxiv");
    expect(container.textContent).toContain("not available");
    fireEvent.change(screen.getByLabelText("Search skills"), { target: { value: "forecast" } });
    expect(screen.queryByText("arxiv")).toBeNull();
    expect(screen.getByText("weather")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Instructions"), { target: { value: "  Be brief.  " } });
    fireEvent.click(screen.getByRole("button", { name: "Save instructions" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("applies from your next message"));
    expect(calls).toContain("PUT /api/me/personality");
    expect((screen.getByLabelText("Instructions") as HTMLTextAreaElement).value).toBe("Be brief.");
  });

  it("says so when no skills are available", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => (path === "/api/capabilities" ? json(200, { ...inventory, capabilities: [{ id: "skills", label: "Skills", status: "unsupported", reason: "none installed" }], skills: [] }) : json(200, { text: "", max: 2000 }))),
    );
    const { container } = render(<MemorySkillsPanel />);
    open(container);
    expect(await screen.findByText(/No skills are available/)).toBeTruthy();
  });
});

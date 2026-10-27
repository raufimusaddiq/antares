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

  it("keeps the editor read-only while a save is pending, so nothing typed is lost", async () => {
    let answer!: (r: Response) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn((path: string, init?: RequestInit) => {
        if (path === "/api/capabilities") return Promise.resolve(json(200, inventory));
        if (init?.method === "PUT") return new Promise<Response>((r) => (answer = r));
        return Promise.resolve(json(200, { text: "", max: 2000 }));
      }),
    );
    const { container } = render(<MemorySkillsPanel />);
    open(container);
    await screen.findByText("arxiv");
    const box = screen.getByLabelText("Instructions") as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "first" } });
    fireEvent.click(screen.getByRole("button", { name: "Save instructions" }));
    await waitFor(() => expect(box.readOnly).toBe(true));
    answer(json(200, { text: "first", max: 2000 }));
    await waitFor(() => expect(box.readOnly).toBe(false));
    expect(box.value).toBe("first");
  });

  it("a reopen after a failed read shows the skills", async () => {
    let fail = true;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        if (path === "/api/capabilities") return fail ? json(503, { error: { code: "HERMES_UNAVAILABLE", message: "x" } }) : json(200, inventory);
        return json(200, { text: "", max: 2000 });
      }),
    );
    const { container } = render(<MemorySkillsPanel />);
    open(container);
    expect(await screen.findByRole("alert")).toBeTruthy();
    fail = false;
    const d = container.querySelector("details")!;
    d.open = false;
    fireEvent(d, new Event("toggle"));
    open(container);
    expect(await screen.findByText("arxiv")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("the editor is read-only until the instructions are read, so nothing typed is replaced", async () => {
    let answer!: (r: Response) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn((path: string) => (path === "/api/capabilities" ? Promise.resolve(json(200, inventory)) : new Promise<Response>((r) => (answer = r)))),
    );
    const { container } = render(<MemorySkillsPanel />);
    open(container);
    const box = screen.getByLabelText("Instructions") as HTMLTextAreaElement;
    expect(box.readOnly).toBe(true);
    answer(json(200, { text: "stored", max: 2000 }));
    await waitFor(() => expect(box.readOnly).toBe(false));
    expect(box.value).toBe("stored");
  });

  it("a failed re-read leaves Save off", async () => {
    let failRead = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        if (path === "/api/capabilities") return json(200, inventory);
        return failRead ? json(503, { error: { code: "HERMES_UNAVAILABLE", message: "x" } }) : json(200, { text: "old", max: 2000 });
      }),
    );
    const { container } = render(<MemorySkillsPanel />);
    open(container);
    const box = () => screen.getByLabelText("Instructions") as HTMLTextAreaElement;
    await waitFor(() => expect(box().value).toBe("old"));
    failRead = true;
    const d = container.querySelector("details")!;
    d.open = false;
    fireEvent(d, new Event("toggle"));
    open(container);
    await screen.findByText("arxiv");
    await new Promise((r) => setTimeout(r, 20));
    fireEvent.change(box(), { target: { value: "edited" } });
    expect((screen.getByRole("button", { name: "Save instructions" }) as HTMLButtonElement).disabled).toBe(true);
  });

  for (const outcome of ["saved", "failed"] as const) {
    it(`a reopen during a pending save reads nothing over the edit (save ${outcome})`, async () => {
      let put: ((r: Response) => void) | null = null;
      let reads = 0;
      vi.stubGlobal(
        "fetch",
        vi.fn((path: string, init?: RequestInit) => {
          if (path === "/api/capabilities") return Promise.resolve(json(200, inventory));
          if (init?.method === "PUT") return new Promise<Response>((r) => (put = r));
          reads++;
          return Promise.resolve(json(200, { text: "old", max: 2000 }));
        }),
      );
      const { container } = render(<MemorySkillsPanel />);
      open(container);
      const box = () => screen.getByLabelText("Instructions") as HTMLTextAreaElement;
      await waitFor(() => expect(box().value).toBe("old"));
      fireEvent.change(box(), { target: { value: "new" } });
      fireEvent.click(screen.getByRole("button", { name: "Save instructions" }));
      await waitFor(() => expect(put).not.toBeNull());
      const d = container.querySelector("details")!;
      d.open = false;
      fireEvent(d, new Event("toggle"));
      open(container); // reopened while the save is pending
      await screen.findByText("arxiv");
      expect(reads).toBe(1); // no second read
      expect(box().value).toBe("new");
      put!(outcome === "saved" ? json(200, { text: "new", max: 2000 }) : json(503, { error: { code: "HERMES_UNAVAILABLE", message: "x" } }));
      await waitFor(() => expect(box().readOnly).toBe(false));
      expect(box().value).toBe("new"); // saved, or kept to retry
      if (outcome === "failed") {
        expect(screen.getByRole("status").textContent).toContain("Not saved");
        expect((screen.getByRole("button", { name: "Save instructions" }) as HTMLButtonElement).disabled).toBe(false);
      }
    });
  }

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

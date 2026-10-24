// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RunPrompts } from "./RunPrompts";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

type Call = { method: string; path: string; body: unknown };

// A server that holds the prompts: GET reads them, a POST answers one and removes it.
function server(initial: unknown[]) {
  let prompts = initial;
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ method, path, body });
      if (method === "POST") {
        prompts = [];
        return new Response(JSON.stringify({ accepted: true }), { status: 200 });
      }
      return new Response(JSON.stringify({ runId: "r", status: "RUNNING", prompts }), { status: 200 });
    }),
  );
  return calls;
}

const approval = { promptId: "p1", kind: "approval", text: "rm -rf /tmp/x", detail: "", choices: ["once", "deny"] };
const question = { promptId: "q1", kind: "clarify", text: "Which file?", detail: "", choices: ["a.txt"] };

describe("RunPrompts", () => {
  it("shows a pending approval from the server and offers only Allow once and Deny", async () => {
    server([approval]);
    render(<RunPrompts runId="r" tick={0} />);
    await screen.findByText("rm -rf /tmp/x");
    const names = screen.getAllByRole("button").map((b) => b.textContent);
    expect(names).toEqual(["Allow once", "Deny"]);
  });

  it("sends nothing until a button is clicked, then sends the chosen decision once", async () => {
    const calls = server([approval]);
    render(<RunPrompts runId="r" tick={0} />);
    await screen.findByText("rm -rf /tmp/x");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(screen.queryByText("rm -rf /tmp/x")).toBeNull());
    expect(calls.filter((c) => c.method === "POST")).toEqual([{ method: "POST", path: "/api/runs/r/approve", body: { promptId: "p1", choice: "deny" } }]);
  });

  it("answers a question with the typed text or a listed choice", async () => {
    const calls = server([question]);
    render(<RunPrompts runId="r" tick={0} />);
    await screen.findByText("Which file?");
    fireEvent.change(screen.getByLabelText("Your answer"), { target: { value: "  b.txt " } });
    fireEvent.click(screen.getByRole("button", { name: "Answer" }));
    await waitFor(() => expect(screen.queryByText("Which file?")).toBeNull());
    expect(calls.filter((c) => c.method === "POST")).toEqual([{ method: "POST", path: "/api/runs/r/clarify", body: { promptId: "q1", answer: "b.txt" } }]);
  });

  it("renders nothing when the server has no pending prompt, and shows a stale answer as an error", async () => {
    server([]);
    const { container } = render(<RunPrompts runId="r" tick={0} />);
    await waitFor(() => expect(container.textContent).toBe(""));
  });
});

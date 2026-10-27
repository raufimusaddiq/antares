// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach } from "vitest";
import { RunView } from "./Chat";
import { MAX_TOOL_ROWS, describeTool, newRun, parseEvent, reduce, type LiveRun, type RunEvent } from "./runState";

afterEach(cleanup);

// Events as the server sends them, parsed exactly as the browser parses them.
const ev = (name: string, data: object = {}): RunEvent => {
  const e = parseEvent(name, JSON.stringify(data));
  if (!e) throw new Error(`unparsed ${name}`);
  return e;
};
const play = (events: RunEvent[], from: LiveRun = newRun("r1")) => events.reduce(reduce, from);
const started = (id: string, label = "lookup") => ev("tool.started", { toolCallId: id, label });
const ended = (id: string, extra: object = {}) => ev("tool.completed", { toolCallId: id, summary: "", failed: false, durationMs: null, ...extra });
const noop = () => {};

describe("tool activity is truthful (#163)", () => {
  it("start, end with duration, failure, and progress order", () => {
    const s = play([started("a", "Web search"), started("b", "Read page"), ended("a", { durationMs: 1500, summary: "3 results" }), ended("b", { failed: true, durationMs: 80 })]);
    expect(s.tools.map((t) => [t.label, t.state, t.durationMs, t.summary])).toEqual([
      ["Web search", "done", 1500, "3 results"],
      ["Read page", "failed", 80, ""],
    ]);
    expect(describeTool(s.tools[0], s.phase)).toBe("done in 1.5 s");
    expect(describeTool(s.tools[1], s.phase)).toBe("failed in 80 ms");
  });

  it("a start with no id, or an end with no matching start, is untracked and never done", () => {
    const s = play([started(""), ended("ghost", { summary: "x" }), ended("")]);
    expect(s.tools.map((t) => t.state)).toEqual(["untracked", "untracked", "untracked"]);
    for (const t of s.tools) expect(describeTool(t, s.phase)).toMatch(/not tracked/);
  });

  it("a duplicate start untracks the earlier running row instead of hiding it; a finished row keeps its outcome", () => {
    const s = play([started("a"), ended("a"), started("a"), started("a"), ended("a")]);
    expect(s.tools.map((t) => t.state)).toEqual(["done", "untracked", "done"]);
  });

  it("run.resumed clears tools and the thinking stage as well as text, so a replay leaves no stale rows", () => {
    const before = play([ev("run.thinking"), started("a"), ev("assistant.delta", { text: "abc" })]);
    const s = play([ev("run.resumed"), started("a"), ended("a")], before);
    expect(s.text).toBe("");
    expect(s.tools).toHaveLength(1);
    expect(s.tools[0].state).toBe("done");
    expect(s.thinking).toBe(false);
  });

  it("an unknown server event changes nothing", () => {
    expect(parseEvent("tool.progress", "{}")).toBeNull();
    expect(parseEvent("reasoning.available", '{"text":"secret"}')).toBeNull();
  });

  it("thinking is a stage only, cleared by the first answer text", () => {
    const thinking = play([ev("run.thinking", { text: "private" })]);
    expect(thinking.thinking).toBe(true);
    expect(JSON.stringify(thinking)).not.toContain("private");
    expect(play([ev("assistant.delta", { text: "hi" })], thinking).thinking).toBe(false);
  });

  it("a noisy stream stays bounded: finished rows fold into counts, running rows are kept", () => {
    const events: RunEvent[] = [started("keep")];
    for (let i = 0; i < 500; i++) events.push(started(`t${i}`), ended(`t${i}`, { failed: i % 10 === 0 }));
    const s = play(events);
    expect(s.tools.length).toBeLessThanOrEqual(MAX_TOOL_ROWS);
    expect(s.tools[0]).toMatchObject({ id: "keep", state: "running" });
    expect(s.folded.count + s.tools.length).toBe(501);
    expect(s.folded.failed + s.tools.filter((t) => t.state === "failed").length).toBe(50);
  });

  it("after the reply ends, a call with no reported end says so; after a cancel it may still be running", () => {
    const open = play([started("a")]);
    expect(describeTool(open.tools[0], "streaming")).toBe("running");
    expect(describeTool(open.tools[0], "completed")).toBe("no end reported");
    expect(describeTool(open.tools[0], "failed")).toBe("no end reported");
    expect(describeTool(open.tools[0], "cancelled")).toMatch(/may still be running/);
  });

  it("parses failure and duration strictly", () => {
    expect(parseEvent("tool.completed", '{"toolCallId":"a","failed":"yes","durationMs":-5}')).toEqual({ type: "tool.completed", id: "a", summary: "", failed: false, durationMs: null });
  });
});

describe("the activity group", () => {
  it("shows Working, then Activity, then the answer, open while the reply runs", () => {
    const s = play([started("a", "Web search")]);
    const { container } = render(<RunView run={s} onCancel={noop} onRetry={noop} onReconnect={noop} />);
    expect(screen.getByText("Working…")).toBeTruthy();
    const group = container.querySelector("details.activity") as HTMLDetailsElement;
    expect(group.open).toBe(true);
    expect(group.querySelector("summary")!.textContent).toBe("Activity: 1 tool call");
    expect(screen.getByText(/Web search: running/)).toBeTruthy();
  });

  it("says Thinking while the assistant reasons and nothing is written yet", () => {
    render(<RunView run={play([ev("run.thinking")])} onCancel={noop} onRetry={noop} onReconnect={noop} />);
    expect(screen.getByText("Thinking…")).toBeTruthy();
  });

  it("folds after the reply ends and opens again on request, with failures counted", () => {
    const s = play([started("a"), ended("a", { failed: true }), ev("run.completed", { status: "COMPLETED", output: "answer" })]);
    const { container } = render(<RunView run={s} onCancel={noop} onRetry={noop} onReconnect={noop} />);
    const group = container.querySelector("details.activity") as HTMLDetailsElement;
    expect(group.open).toBe(false);
    expect(group.querySelector("summary")!.textContent).toBe("Activity: 1 tool call, 1 failed");
    // Folded, not removed: the rows are still there for the reader who opens it, and the answer follows.
    expect(group.querySelector('li[data-tool="failed"]')).toBeTruthy();
    expect(group.hasAttribute("open")).toBe(false);
    expect(screen.getByText("answer")).toBeTruthy();
  });
});

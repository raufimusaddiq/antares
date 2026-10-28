// @vitest-environment jsdom
// Native OpenUI (the openuiChatLibrary of @openuidev/react-ui): the gate, whole-reply detection, and the two
// built-in actions, which only ever reach the composer or a confirmed new tab.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { openuiChatExamples } from "@openuidev/react-ui/genui-lib";
import { ALLOWED_ACTIONS, followUpText, runAction, safeUrl } from "./actions";
import RichBlock from "./Block";
import { ReplyContext } from "./reply";
import { MAX_BLOCK_CHARS, splitSegments } from "./segments";
import { check } from "./validate";

beforeAll(() => {
  // jsdom has no layout observers; the charts and carousels ask for them.
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as never;
  globalThis.IntersectionObserver ??= class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } } as never;
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

/** OpenUI's own example programs, without their prose preamble. */
const examples = openuiChatExamples.map((e) => e.slice(e.indexOf("root =")).split(/\n\s*\n(?=[A-Z][^=\n]*$)/m)[0].trim());

describe("the gate over the chat library", () => {
  it("accepts OpenUI's own example programs", () => {
    for (const src of examples) expect(check(src), src.slice(0, 60)).toEqual({ ok: true });
  });
  it("refuses what is not a complete, plain program of the library", () => {
    expect(check('root = Card([Bogus("x")])').ok).toBe(false);
    expect(check('root = Card([TextContent("x")').ok).toBe(false); // incomplete
    expect(check('root = TextContent("x")').ok).toBe(false); // not the library's root
    expect(check('$n = 0\nroot = Card([TextContent("x")])').ok).toBe(false); // state
    expect(check(`root = Card([TextContent("${"a".repeat(20_001)}")])`).ok).toBe(false);
  });
});

describe("whole-reply detection", () => {
  it("a reply that is a program is one OpenUI segment; prose stays Markdown", () => {
    expect(splitSegments('  root = Card([TextContent("hi")])')).toEqual([{ kind: "openui", source: 'root = Card([TextContent("hi")])', closed: true }]);
    expect(splitSegments("The root = cause is here.")).toEqual([{ kind: "md", text: "The root = cause is here." }]);
    expect(splitSegments("root = " + "x".repeat(MAX_BLOCK_CHARS))[0].kind).toBe("md");
  });
});

describe("actions", () => {
  it("are exactly reply, continue_conversation and open_url", () => {
    expect(ALLOWED_ACTIONS).toEqual(["reply", "continue_conversation", "open_url"]);
    expect(runAction("constructor", {}, {})).toEqual({ ok: false, reason: "action not allowed" });
  });
  it("turns a follow-up into composer text, with form values as JSON", () => {
    expect(followUpText({ type: "continue_conversation", params: { message: "Tell me more" }, humanFriendlyMessage: "x" })).toBe("Tell me more");
    expect(followUpText({ type: "continue_conversation", params: {}, humanFriendlyMessage: "Plan it", formState: { days: 3 } })).toBe('Plan it\n\n```json\n{\n  "days": 3\n}\n```');
    const reply = vi.fn();
    expect(runAction("continue_conversation", { params: { message: "Go" } }, { reply })).toEqual({ ok: true });
    expect(reply).toHaveBeenCalledWith("Go");
  });
  it("opens only absolute http(s) addresses without credentials", () => {
    expect(safeUrl({ params: { url: "https://example.com/a" } })).toBe("https://example.com/a");
    for (const url of ["javascript:alert(1)", "data:text/html,x", "/api/admin/users", "https://u:p@example.com", "file:///etc/passwd"]) {
      expect(safeUrl({ params: { url } }), url).toBeNull();
    }
    const open = vi.fn();
    expect(runAction("open_url", { params: { url: "javascript:alert(1)" } }, { open })).toEqual({ ok: false, reason: "invalid url" });
    expect(open).not.toHaveBeenCalled();
  });
});

describe("rendering", () => {
  it("renders a chat-library program; a follow-up button fills the composer and sends nothing", () => {
    const add = vi.fn();
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    render(
      <ReplyContext.Provider value={add}>
        <RichBlock source={'root = Card([TextContent("Hello there"), btns])\nbtns = Buttons([Button("Tell me more", @ToAssistant("Tell me more about it"))])'} />
      </ReplyContext.Provider>,
    );
    expect(screen.getByText("Hello there")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Tell me more" }));
    expect(add).toHaveBeenCalledWith("Tell me more about it");
    expect(fetchSpy).not.toHaveBeenCalled();
  });
  it("shows an invalid finished program as its source, not a half-built view", () => {
    render(<RichBlock source={'root = Card([Bogus("x")])'} />);
    expect(document.body.textContent).toContain('Bogus("x")');
  });
});

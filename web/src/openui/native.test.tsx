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
import { check, MAX_ARRAY } from "./validate";

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
    // Some examples show pictures, which Antares refuses (see the image test); every other one is accepted.
    const verdicts = examples.map((src) => check(src));
    for (const [i, v] of verdicts.entries()) {
      if (!v.ok && v.reason === "images are not allowed") continue;
      expect(v, examples[i].slice(0, 60)).toEqual({ ok: true });
    }
    expect(verdicts.filter((v) => v.ok).length).toBeGreaterThanOrEqual(2);
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
  it("a model-labelled action never reads as completed: after the click it says nothing was sent (#173)", () => {
    const add = vi.fn();
    render(
      <ReplyContext.Provider value={add}>
        <RichBlock source={'root = Card([Buttons([Button("Submit order", @ToAssistant("Order 2 coffees"))])])'} />
      </ReplyContext.Provider>,
    );
    expect(screen.queryByRole("status")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Submit order" }));
    expect(add).toHaveBeenCalledWith("Order 2 coffees");
    expect(screen.getByRole("status").textContent).toBe("Added to your message. Nothing was sent.");
  });
  it("a finished program with an unused statement is shown as source, not with that part silently dropped", () => {
    const source = 'root = Card([TextContent("kept")])\nlost = TextContent("this part is not referenced")';
    expect(check(source)).toEqual({ ok: false, reason: "unused statement" });
    render(<RichBlock source={source} />);
    expect(document.body.textContent).toContain("this part is not referenced");
    expect(document.querySelector(".ou-block")).toBeNull();
  });
  it("checks a streaming program before rendering it: unsafe or oversized partial output is never rendered", () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    const partial = "root = Card([TextContent(\"Hi\"), t])\nt = TextContent(\"unfinished";
    expect(check(partial, true)).toEqual({ ok: true });
    expect(check(partial).ok).toBe(false);
    const unsafe = [
      'root = Card([TextContent("x")])\n$n = 1\nmore = TextContent("y',
      'root = Card([TextContent("x"), q])\nq = Query("tool", {})\nmore = TextContent("y',
      'root = Card([Bogus("x"), TextContent("y',
      `root = Card([${Array.from({ length: MAX_ARRAY + 1 }, () => 'TextContent("a")').join(", ")}, TextContent("y`,
      'root = Stack([Text("old',
    ];
    for (const source of unsafe) {
      expect(check(source, true).ok, source).toBe(false);
      const { container, unmount } = render(<RichBlock source={source} streaming />);
      expect(container.querySelector(".ou-block"), source).toBeNull();
      expect(container.querySelector(".ou-fallback"), source).not.toBeNull();
      unmount();
    }
    expect(fetchSpy).not.toHaveBeenCalled();
  });
  it("never renders an image: no image address, gallery, background or Markdown image, final or streaming", () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    const images = [
      'root = Card([ImageBlock("/api/admin/users")])',
      'root = Card([Image("/api/admin/users", "x")])',
      'root = Card([ImageGallery([{src: "/api/admin/users", alt: "x"}])])',
      'root = Card([ListBlock([ListItem("t", "s", {src: "/api/me", alt: "x"})])])',
      'root = Card([TextContent("see ![x](/api/admin/users)")])',
      'root = Card([MarkDownRenderer("![x](https://example.com/a.png)")])',
    ];
    for (const source of images) {
      expect(check(source), source).toEqual({ ok: false, reason: "images are not allowed" });
      expect(check(source + "\nmore = TextContent(\"y", true).ok, source).toBe(false);
      const { container, unmount } = render(<RichBlock source={source} />);
      expect(container.querySelector("img"), source).toBeNull();
      unmount();
    }
    expect(fetchSpy).not.toHaveBeenCalled();
  });
  it("accepts absolute http(s) links outside Antares, in Markdown and in a Card's sources, and refuses any other", () => {
    const refused = { ok: false, reason: "links must be absolute http(s) addresses outside Antares" };
    const own = `${window.location.origin}/api/admin/users`;
    expect(check('root = Card([TextContent("[docs](https://example.com/docs)")])')).toEqual({ ok: true });
    for (const ok of ["[mail](mailto:a@b.c)", "write to a@b.c", "see <https://example.com/a>", "see https://example.com/a"]) {
      expect(check(`root = Card([TextContent(${JSON.stringify(ok)})])`), ok).toEqual({ ok: true });
    }
    const links = ["[x](/api/admin/users)", "[x](javascript:alert(1))", "[x][r]\n\n[r]: /api/me", `[x](${own})`, `<${own}>`, `see ${own} now`];
    for (const link of [...links, ...links.map((l) => l.replace(window.location.origin, window.location.origin.toUpperCase()))]) {
      expect(check(`root = Card([TextContent(${JSON.stringify(link)})])`), link).toEqual(refused);
    }
    const sources = (url: string) => `root = Card([TextContent("A fact [1]")], [{title: "t", sourceName: "s", url: ${JSON.stringify(url)}}])`;
    expect(check(sources("https://example.com/a"))).toEqual({ ok: true });
    for (const url of ["/api/admin/users", "javascript:alert(1)", "https://u:p@example.com", own]) {
      expect(check(sources(url)), url).toEqual(refused);
      expect(check(sources(url) + '\nmore = TextContent("y', true).ok, url).toBe(false);
      const { container, unmount } = render(<RichBlock source={sources(url)} />);
      expect(container.querySelector(".ou-block"), url).toBeNull();
      unmount();
    }
    expect(check('root = Card([TextContent("[x](htt', true)).toEqual({ ok: true }); // still arriving
    expect(safeUrl({ params: { url: own } })).toBeNull();
  });
  it("labels a block from the earlier Antares component set instead of calling it broken", () => {
    render(<RichBlock source={'root = Stack([Text("old answer")])'} />);
    expect(document.body.textContent).toContain("earlier version of rich replies");
    expect(document.body.textContent).toContain('Text("old answer")');
  });
  it("shows an invalid finished program as its source, not a half-built view", () => {
    render(<RichBlock source={'root = Card([Bogus("x")])'} />);
    expect(document.body.textContent).toContain('Bogus("x")');
  });
});

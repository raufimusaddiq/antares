// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DocView } from "./DocView";
import ReaderBlock from "./ReaderBlock";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";
import { READER_COMPONENTS } from "../openui/readerLibrary";

// jsdom has no layout: the charts and tables of the library measure with a ResizeObserver.
class NoopObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
beforeEach(() => {
  vi.stubGlobal("ResizeObserver", NoopObserver);
  Element.prototype.scrollTo = () => {}; // jsdom has no scrolling; the tabs scroll the active one into view
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const FALLBACK = "Plain version of the block.";
const card = (body: string) => `root = Card([x])\nx = ${body}`;

const noNetwork = () => {
  const f = vi.fn(async () => new Response("{}"));
  vi.stubGlobal("fetch", f);
  const open = vi.spyOn(window, "open").mockImplementation(() => null);
  return { f, open };
};

describe("the reader renderer", () => {
  it("draws a valid block", async () => {
    render(<ReaderBlock markup={card('Table([Col("City", ["Jakarta"]), Col("People", [11000000], "number")])')} fallback={FALLBACK} />);
    await waitFor(() => expect(screen.getByText("Jakarta")).toBeTruthy());
    expect(screen.queryByText(FALLBACK)).toBeNull();
  });

  // Everything a reader must not be able to do is refused before it is drawn, and the post shows the plain version. No request is
  // made and nothing is opened, whatever the markup says.
  it.each([
    ["an unregistered component", card('Marquee("hi")')],
    ["a form", `root = Card([f])\nf = Form("f", b, [])\nb = Buttons([])`],
    ["a button with an action", card('Buttons([Button("Go", @OpenUrl("https://example.org/"))])')],
    ["a button that would reach an admin route", card('Button("x", @ToAssistant("/admin/users"))')],
    ["an image", card('Image("alt", "https://example.org/a.png")')],
    ["an image in a list item", card('ListBlock([ListItem("t", "s", {src: "https://example.org/a.png", alt: "a"})])')],
    ["a follow-up", card('FollowUpBlock([FollowUpItem("tell me more")])')],
    ["a carousel", card('Carousel([[TextContent("a")]])')],
    ["a query", `root = Card([x])\nx = TextContent(q.text)\nq = Query("tool", {}, {})`],
    ["state", `$value = "x"\nroot = Card([x])\nx = TextContent($value)`],
    ["a link outside the card's own sources that points at this site", card('TextContent("[x](/api/admin/users)")')],
    ["a javascript link", card('TextContent("[x](javascript:alert(1))")')],
  ])("refuses %s and shows the plain version, with no request and nothing opened", async (_name, markup) => {
    const { f, open } = noNetwork();
    render(<ReaderBlock markup={markup} fallback={FALLBACK} />);
    await waitFor(() => expect(screen.getByText(FALLBACK)).toBeTruthy());
    expect(f).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
  });

  it("shows the plain version when the markup is not a program at all", async () => {
    render(<ReaderBlock markup={"this is not openui"} fallback={FALLBACK} />);
    await waitFor(() => expect(screen.getByText(FALLBACK)).toBeTruthy());
  });
});

it("draws a card with sources without fetching anything from another site (no favicon strip)", async () => {
  const { f } = noNetwork();
  const markup = `root = Card([t], [{url: "https://example.org/data", title: "The data", sourceName: "example.org"}])\nt = TextContent("A claim [1].")`;
  const { container } = render(<ReaderBlock markup={markup} fallback={FALLBACK} />);
  await screen.findByText(/A claim/);
  // The chat card's strip carries an icon per source, fetched from a third party; the reader's card has none, and the
  // post's own sources line (below) is the one place sources are listed.
  expect(container.querySelectorAll("img").length).toBe(0);
  expect(container.querySelector("a[href*='google']")).toBeNull();
  expect(f).not.toHaveBeenCalled();
});

it("labels a block's sources as the assistant's and not checked, because none can be verified", () => {
  const blocks = [{ anchor: 0, markup: 'root = Card([t])\nt = TextContent("x")', fallback: "x", sources: [{ url: "https://example.org/data", title: "The data", retrievedAt: "2026-11-08", verified: false }] }];
  const doc = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "Words" }] }] };
  const { container } = render(<DocView doc={doc} blocks={blocks} />);
  expect(container.textContent).toContain("Sources named by the assistant, not independently checked:");
  expect(container.querySelector("a[href='https://example.org/data']")?.textContent).toBe("example.org");
});

// T059: what a reader may do works, and calls nothing.
describe("reader interaction", () => {
  it("collapses and expands a section on click, with no network call and nothing opened", async () => {
    const { f, open } = noNetwork();
    const markup = `root = Card([acc])
acc = Accordion([i1])
i1 = AccordionItem("one", "More detail", [t1])
t1 = TextContent("Shown when open.")`;
    const { container } = render(<ReaderBlock markup={markup} fallback={FALLBACK} />);
    const trigger = await screen.findByText("More detail");
    const state = () => container.querySelector(".openui-accordion-item")?.getAttribute("data-state");
    const was = state(); // the library opens the first section by itself
    fireEvent.mouseOver(trigger);
    fireEvent.click(trigger);
    await waitFor(() => expect(state()).not.toBe(was));
    fireEvent.click(trigger);
    await waitFor(() => expect(state()).toBe(was));
    expect(f).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
  });

  it("switches tabs without a request", async () => {
    const { f } = noNetwork();
    const markup = `root = Card([tabs])
tabs = Tabs([a, b])
a = TabItem("a", "First", [ta])
b = TabItem("b", "Second", [tb])
ta = TextContent("In the first tab.")
tb = TextContent("In the second tab.")`;
    render(<ReaderBlock markup={markup} fallback={FALLBACK} />);
    fireEvent.mouseDown(await screen.findByText("Second"));
    fireEvent.click(await screen.findByText("Second"));
    await waitFor(() => expect(screen.getByText("In the second tab.")).toBeTruthy());
    expect(f).not.toHaveBeenCalled();
  });
});

// The server's copy of the allowed components (internal/blog/openui.go) and this list must not drift: a component only one
// side knows is a block that is stored and then not shown, or shown and never validated.
describe("the allowed components", () => {
  it("are the same here and on the server", () => {
    const go = readFileSync(join(__dirname, "..", "..", "..", "internal", "blog", "openui.go"), "utf8");
    const body = /var readerComponents = setOfNames\(([\s\S]*?)\n\)/.exec(go);
    expect(body, "readerComponents moved or was renamed in internal/blog/openui.go").not.toBeNull();
    const server = [...(body![1].matchAll(/"([A-Za-z]+)"/g))].map((m) => m[1]).sort();
    expect([...READER_COMPONENTS].sort()).toEqual(server);
  });

  it("all exist in the chat library the reader library is cut from", () => {
    const missing = READER_COMPONENTS.filter((n) => !(n in (openuiChatLibrary.components as Record<string, unknown>)));
    expect(missing, "the reader list names components @openuidev/react-ui no longer has").toEqual([]);
  });
});

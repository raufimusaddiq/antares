import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { SearchResults, Snippet, type SearchState } from "./Search";
import { Transcript } from "./Chat";
import { parseSearch, searchChats, type SearchPage, type Turn } from "./chatApi";
import { reachOf } from "./history";

const noop = () => {};
const page = (over: Partial<SearchPage> = {}): SearchPage => ({ results: [], nextCursor: "", index: { complete: true, chats: 2, pending: 0, partial: 0 }, ...over });
const state = (p: SearchPage | null, over: Partial<SearchState> = {}): SearchState => ({ query: "ünal", page: p, error: "", loading: false, params: null, ...over });
const render = (s: SearchState) => renderToStaticMarkup(<SearchResults state={s} onOpen={noop} onMore={noop} />);

const hit = {
  chatId: "a".repeat(32),
  chatTitle: "Plan",
  kind: "message",
  turn: 4,
  role: "assistant",
  snippet: [{ text: "see " }, { text: "Ünal", hit: true }, { text: " now" }],
};

describe("snippets are text, never markup", () => {
  it("marks the matched span and escapes everything else", () => {
    const html = renderToStaticMarkup(
      <Snippet segments={[{ text: "<img src=x onerror=alert(1)> ", hit: false }, { text: "<script>alert(1)</script>", hit: true }]} />,
    );
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<script");
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
    expect(html).toContain("<mark>&lt;script&gt;alert(1)&lt;/script&gt;</mark>");
  });

  it("renders a result with the chat title, who wrote it and the marked span", () => {
    const html = render(state(parseSearch({ results: [hit], nextCursor: "", index: { complete: true, chats: 1, pending: 0, partial: 0 } })));
    expect(html).toContain("Plan");
    expect(html).toContain("Assistant");
    expect(html).toContain("<mark>Ünal</mark>");
  });
});

describe("search says what it knows", () => {
  it("no match is a plain no match", () => {
    const html = render(state(page()));
    expect(html).toContain("No matches for");
    expect(html).not.toContain("indexing");
  });

  it("a partial index is never presented as a complete answer", () => {
    const none = render(state(page({ index: { complete: false, chats: 5, pending: 2, partial: 0 } })));
    expect(none).toContain("No matches yet");
    expect(none).toContain("Still indexing 2 of 5");
    const some = render(state(page({ results: [{ ...hit, kind: "message" } as never], index: { complete: false, chats: 5, pending: 2, partial: 0 } })));
    expect(some).toContain("may be incomplete");
  });

  it("a chat cut by the size limit is partly indexed, not still indexing", () => {
    const html = render(state(page({ index: { complete: false, chats: 4, pending: 0, partial: 1 } })));
    expect(html).toContain("1 of 4 conversations are only partly indexed");
    expect(html).not.toContain("Still indexing");
    expect(html).not.toContain("search again");
  });

  it("an error shows words from the vocabulary, not the server text, and no results", () => {
    const html = render(state(null, { error: "TENANT_NOT_READY" }));
    expect(html).toContain("Search unavailable");
    expect(html).toContain("workspace is not available");
    expect(html).not.toContain("No matches");
  });

  it("offers more only when the server has more", () => {
    expect(render(state(page({ nextCursor: "c" })))).toContain("More results");
    expect(render(state(page()))).not.toContain("More results");
  });
});

describe("parseSearch refuses what it cannot place", () => {
  const ok = { results: [hit], nextCursor: "", index: { complete: true, chats: 1, pending: 0, partial: 0 } };
  it("accepts a well-formed page and keeps the turn anchor", () => {
    expect(parseSearch(ok)?.results[0]).toMatchObject({ chatId: "a".repeat(32), turn: 4, kind: "message" });
  });
  it("a title hit has no turn", () => {
    expect(parseSearch({ ...ok, results: [{ ...hit, kind: "title", role: "title", turn: undefined }] })?.results[0].turn).toBeNull();
  });
  it("a message without a usable anchor, or any malformed shape, is a failure", () => {
    for (const bad of [{ ...hit, turn: undefined }, { ...hit, turn: -1 }, { ...hit, turn: 1.5 }, { ...hit, role: "system" }, { ...hit, snippet: [{ hit: true }] }, { ...hit, chatId: "" }]) {
      expect(parseSearch({ ...ok, results: [bad] }), JSON.stringify(bad)).toBeNull();
    }
    expect(parseSearch({ results: [] })).toBeNull();
    expect(parseSearch(null)).toBeNull();
  });
});

describe("the request carries text, filters and a cursor only", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("builds the global and the per-chat URL", async () => {
    const f = vi.fn(async () => new Response(JSON.stringify(page()), { status: 200 }));
    vi.stubGlobal("fetch", f);
    await searchChats("Ünal & co", { role: "user", cursor: "c1" });
    await searchChats("x y", { chatId: "b".repeat(32) });
    const urls = f.mock.calls.map((c) => String((c as unknown[])[0]));
    expect(urls[0]).toBe("/api/search/chats?q=%C3%9Cnal+%26+co&role=user&cursor=c1");
    expect(urls[1]).toBe(`/api/chats/${"b".repeat(32)}/search?q=x+y`);
  });
});

describe("opening a result at its turn", () => {
  const turns: Turn[] = Array.from({ length: 6 }, (_, i) => ({ seq: i + 10, role: i % 2 ? "assistant" : "user", text: `m${i + 10}` }));
  it("says whether the turn is held, needs an older page, or is gone", () => {
    expect(reachOf([], "", 12)).toBe("wait");
    expect(reachOf(turns, "cur", 12)).toBe("held");
    expect(reachOf(turns, "cur", 3)).toBe("older");
    expect(reachOf(turns, "", 3)).toBe("gone"); // start of the conversation reached, turn not there
    expect(reachOf([turns[0], turns[2]], "cur", 11)).toBe("gone"); // a hole where the turn should be
  });

  it("marks the focused turn", () => {
    const props = { pending: null, run: null, onCancel: noop, onRetry: noop, onReconnect: noop, onResend: noop };
    const html = renderToStaticMarkup(<Transcript {...props} turns={turns} focusSeq={12} />);
    expect(html.match(/data-found="true"/g)?.length).toBe(1);
    expect(html).toContain('aria-current="location"');
    expect(renderToStaticMarkup(<Transcript {...props} turns={turns} />)).not.toContain("data-found");
  });
});

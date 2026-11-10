// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Screen, type View } from "../Screen";
import type { AccountState } from "../api";
import { LatestPosts } from "./LatestPosts";
import { PublicIndex, PublicPostPage } from "./Public";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const sum = (n: number, over: Record<string, unknown> = {}) => ({ slug: `post-${n}`, title: `Post ${n}`, summary: `About ${n}`, coverUrl: "", authorName: "A Writer", publishedAt: "2026-11-08T01:00:00Z", ...over });
const ME = (state: AccountState) => ({ email: "r@example.test", displayName: "Reader", requestedAt: "", state, canAdmin: false });
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

/** A router for the few addresses these pages call. `me` null means a visitor (401); `mine` is the reader's own post list. */
function server(o: { me?: AccountState | null; list?: (url: URL) => unknown[] | number; mine?: unknown[]; writing?: boolean } = {}) {
  const seen: string[] = [];
  const f = vi.fn(async (input: RequestInfo | URL) => {
    const u = new URL(String(input), "https://antares.test");
    seen.push(u.pathname + u.search);
    if (u.pathname === "/api/me") return o.me ? json(200, ME(o.me)) : json(401, {});
    if (u.pathname.startsWith("/api/public/blog/posts/")) return json(200, { title: "A post", summary: "s", coverUrl: "", doc: { type: "doc", content: [] }, blocks: [], authorName: "A Writer", publishedAt: "2026-11-08T01:00:00Z" });
    if (u.pathname === "/api/public/blog/posts") {
      const l = o.list ? o.list(u) : [];
      return typeof l === "number" ? json(l, {}) : json(200, l);
    }
    if (u.pathname === "/api/blog/posts") return o.me === "ACTIVE" ? json(200, o.mine ?? []) : json(401, {});
    if (u.pathname === "/api/blog/capabilities") return json(200, { writing: o.writing === true, check: false, webSearch: false });
    return json(404, {});
  });
  vi.stubGlobal("fetch", f);
  return seen;
}

describe("the latest posts on the sign-in and waiting screens", () => {
  it("shows up to three posts as cards that each open the post", async () => {
    server({ list: () => [sum(1), sum(2), sum(3), sum(4)] });
    render(<LatestPosts />);
    const links = await screen.findAllByRole("link");
    expect(links.map((a) => a.getAttribute("href"))).toEqual(["/b/post-1", "/b/post-2", "/b/post-3"]);
    expect(within(links[0]).getByText("Post 1")).toBeTruthy();
    expect(within(links[0]).getByText(/A Writer · /)).toBeTruthy();
  });
  it.each([
    ["is empty", () => []],
    ["fails", () => 500],
    ["is rate limited", () => 429],
  ])("is simply absent when the list %s", async (_n, list) => {
    const seen = server({ list });
    const { container } = render(<LatestPosts />);
    await waitFor(() => expect(seen.some((s) => s.startsWith("/api/public/blog/posts"))).toBe(true));
    await new Promise((r) => setTimeout(r, 20));
    expect(container.innerHTML).toBe("");
  });
});

describe("the blog button on every screen that is not the chat", () => {
  const html = (view: View) => renderToStaticMarkup(<Screen view={view} onRefresh={() => {}} onSignOut={() => {}} />);
  const acct = (state: AccountState): View => ({ screen: "account", account: { email: "p@example.test", displayName: "P", requestedAt: "", state, canAdmin: false } });
  it.each([
    ["sign-in", { screen: "login" } as View],
    ["sign-in after a failure", { screen: "login-failed" } as View],
    ["waiting", acct("PENDING")],
    ["refused", acct("REJECTED")],
    ["suspended", acct("SUSPENDED")],
  ])("%s offers Read the blog, and nothing else of the blog", (_n, view) => {
    const out = html(view);
    expect(out).toContain('href="/b"');
    expect(out).toContain("Read the blog");
    expect(out).not.toContain('href="/blog"');
  });
  it("a broken workspace has nothing to read from", () => {
    for (const v of [acct("ERROR"), acct("SETTING_UP"), { screen: "unreachable" } as View]) expect(html(v)).not.toContain("Read the blog");
  });
  it("the sign-in action stays the first control", () => {
    const out = html({ screen: "login" });
    expect(out.indexOf("Sign in with Google")).toBeLessThan(out.indexOf("Read the blog"));
  });
});

describe("who the public pages think is reading", () => {
  it("a visitor is offered Sign in", async () => {
    server({ me: null });
    render(<PublicIndex />);
    expect(await screen.findByRole("link", { name: "Sign in with Google" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "Back to Antares" })).toBeNull();
  });
  it.each(["PENDING", "SUSPENDED", "ACTIVE"] as AccountState[])("an account (%s) is offered a way back, which is home", async (state) => {
    server({ me: state });
    render(<PublicIndex />);
    const back = await screen.findByRole("link", { name: "Back to Antares" });
    expect(back.getAttribute("href")).toBe("/");
    expect(screen.queryByRole("link", { name: "Sign in with Google" })).toBeNull();
  });
  it("a post offers More posts, and a pending account gets no writing or own-post calls", async () => {
    const seen = server({ me: "PENDING" });
    render(<PublicPostPage slug="a-post" />);
    expect(await screen.findByRole("link", { name: "Back to Antares" })).toBeTruthy();
    expect((await screen.findByRole("link", { name: "More posts" })).getAttribute("href")).toBe("/b");
    expect(seen).not.toContain("/api/blog/posts");
    expect(seen).not.toContain("/api/blog/capabilities");
    expect(screen.queryByRole("link", { name: /My.*posts/ })).toBeNull();
  });
  it("an approved writer sees a link to their workspace", async () => {
    server({ me: "ACTIVE", writing: true });
    render(<PublicIndex />);
    expect((await screen.findByRole("link", { name: /My.*posts/ })).getAttribute("href")).toBe("/blog");
  });
  it("an approved reader without writing does not", async () => {
    const seen = server({ me: "ACTIVE", writing: false });
    render(<PublicIndex />);
    await screen.findByRole("link", { name: "Back to Antares" });
    await waitFor(() => expect(seen).toContain("/api/blog/capabilities"));
    expect(screen.queryByRole("link", { name: /My.*posts/ })).toBeNull();
  });
});

describe("Yours", () => {
  it("marks only the posts the signed-in reader published, never drafts", async () => {
    server({
      me: "ACTIVE",
      list: () => [sum(1), sum(2)],
      mine: [{ id: "x", title: "T", status: "published", slug: "post-2", updatedAt: "", publishedAt: "", interactive: false }, { id: "y", title: "D", status: "draft", slug: "post-1", updatedAt: "", publishedAt: "", interactive: false }],
    });
    render(<PublicIndex />);
    await screen.findByText("Post 2");
    await waitFor(() => expect(screen.getAllByText("Yours")).toHaveLength(1));
    expect(within(screen.getByText("Post 2").closest("li") as HTMLElement).getByText("Yours")).toBeTruthy();
  });
  it("no mark for a visitor", async () => {
    server({ me: null, list: () => [sum(1)] });
    render(<PublicIndex />);
    await screen.findByText("Post 1");
    expect(screen.queryByText("Yours")).toBeNull();
  });
});

describe("finding a post", () => {
  it("Show more appears after a full page, appends the next page and never repeats a post", async () => {
    const page1 = Array.from({ length: 20 }, (_, i) => sum(i + 1));
    const page2 = [sum(20), sum(21)]; // post 20 moved down a page while reading
    server({ me: null, list: (u) => (u.searchParams.get("page") === "2" ? page2 : page1) });
    render(<PublicIndex />);
    await screen.findByText("Post 1");
    fireEvent.click(await screen.findByRole("button", { name: "Show more" }));
    await screen.findByText("Post 21");
    expect(screen.getAllByText("Post 20")).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull(); // a short page is the end
  });
  it("a short first page has no Show more", async () => {
    server({ me: null, list: () => [sum(1)] });
    render(<PublicIndex />);
    await screen.findByText("Post 1");
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
  });
  it("searching sends the text, says when nothing matches, and Clear brings everything back", async () => {
    const seen = server({ me: null, list: (u) => (u.searchParams.get("q") === "zzz" ? [] : [sum(1)]) });
    render(<PublicIndex />);
    await screen.findByText("Post 1");
    fireEvent.change(screen.getByRole("searchbox", { name: "Search posts" }), { target: { value: "  zzz " } });
    expect((await screen.findAllByText("No posts match")).length).toBeGreaterThan(0);
    expect(seen.some((s) => s.includes("q=zzz"))).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    await screen.findByText("Post 1");
    expect(screen.queryByRole("button", { name: "Clear" })).toBeNull();
  });
  it("a failed page keeps the posts already shown and can be retried", async () => {
    let fail = false;
    const page1 = Array.from({ length: 20 }, (_, i) => sum(i + 1));
    server({ me: null, list: (u) => (u.searchParams.get("page") === "2" ? (fail ? 500 : [sum(21)]) : page1) });
    render(<PublicIndex />);
    await screen.findByText("Post 1");
    fail = true;
    fireEvent.click(await screen.findByRole("button", { name: "Show more" }));
    await screen.findByText("We could not load more posts.");
    expect(screen.getByText("Post 1")).toBeTruthy();
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("Post 21");
  });
  it("a first-page failure offers Try again and an empty blog says so", async () => {
    let n = 0;
    server({ me: null, list: () => (n++ === 0 ? 500 : []) });
    render(<PublicIndex />);
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await screen.findByText("Nothing published yet");
  });
});

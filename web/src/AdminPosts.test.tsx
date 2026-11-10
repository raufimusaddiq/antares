// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AdminScreens } from "./Admin";
import { parseAdminPost, parseAdminPostPage } from "./adminPostsApi";
import { parseAdminPath } from "./route";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const ID = (n: number) => String(n).padStart(32, "0");
const row = (n: number, over: Record<string, unknown> = {}) => ({ id: ID(n), slug: `post-${n}`, title: `Post ${n}`, summary: `About ${n}`, authorName: "Sari", publishedAt: "2026-11-08T01:00:00.000000000Z", state: "visible", ...over });
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const view = (over: Record<string, unknown> = {}) => ({ ...row(1), coverUrl: "", doc: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "The published body" }] }] }, blocks: [], ...over });

/** The admin post routes, scripted. Every call is recorded so a test can say what was (not) sent. */
function server(o: { list?: (u: URL) => unknown | number; one?: unknown | number; down?: number; restore?: unknown | number } = {}) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = new URL(String(input), "https://antares.test");
      const method = init?.method ?? "GET";
      calls.push({ method, path: u.pathname + u.search, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (u.pathname === "/api/admin/blog-posts") {
        const l = o.list ? o.list(u) : { items: [], next: "" };
        return typeof l === "number" ? json(l, { error: { code: "INTERNAL" } }) : json(200, l);
      }
      if (u.pathname.endsWith("/take-down")) return o.down ? json(o.down, { error: { code: o.down === 409 ? "ALREADY_TAKEN_DOWN" : "INVALID_REQUEST" } }) : json(200, row(1, { state: "taken_down", takedown: { at: "2026-11-09T00:00:00Z", reason: "Spam", byName: "Boss" } }));
      if (u.pathname.endsWith("/restore")) {
        const r = o.restore ?? row(1);
        return typeof r === "number" ? json(r, { error: { code: "NOT_TAKEN_DOWN" } }) : json(200, r);
      }
      if (u.pathname.startsWith("/api/admin/blog-posts/")) return typeof o.one === "number" ? json(o.one, { error: { code: "NOT_FOUND" } }) : json(200, o.one ?? view());
      return json(404, {});
    }),
  );
  return calls;
}

describe("Admin → Posts list", () => {
  it("lists published posts with their state, and a taken-down one with its reason", async () => {
    server({ list: () => ({ items: [row(1), row(2, { state: "hidden_account" }), row(3, { state: "taken_down", takedown: { at: "2026-11-09T00:00:00Z", reason: "Advertising", byName: "Boss" } })], next: "" }) });
    render(<AdminScreens route={{ kind: "posts" }} onNavigate={vi.fn()} />);
    expect(await screen.findByText("Post 1")).toBeTruthy();
    expect([...document.querySelectorAll(".badge")].map((b) => b.textContent)).toEqual(["Visible", "Hidden · account", "Taken down"]);
    expect(screen.getByText(/Advertising/)).toBeTruthy();
    expect(document.body.textContent).toContain("3 posts.");
  });

  it("filters by state and searches, asking the server for each", async () => {
    const calls = server({ list: () => ({ items: [row(1)], next: "" }) });
    render(<AdminScreens route={{ kind: "posts" }} onNavigate={vi.fn()} />);
    await screen.findByText("Post 1");
    fireEvent.click(screen.getByRole("button", { name: "Taken down" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/api/admin/blog-posts?state=taken_down")).toBe(true));
    fireEvent.change(screen.getByLabelText("Search by title or author"), { target: { value: "flights" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/api/admin/blog-posts?state=taken_down&q=flights")).toBe(true));
  });

  it("says nothing matched, nothing is published, and a failure with a retry", async () => {
    server({ list: (u) => (u.searchParams.get("q") ? { items: [], next: "" } : { items: [], next: "" }) });
    const { unmount } = render(<AdminScreens route={{ kind: "posts" }} onNavigate={vi.fn()} />);
    expect(await screen.findByText("Nothing is published yet.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Search by title or author"), { target: { value: "wine" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByText("No posts match “wine”.")).toBeTruthy();
    unmount();
    cleanup();
    let fail = true;
    server({ list: () => (fail ? 500 : { items: [row(1)], next: "" }) });
    render(<AdminScreens route={{ kind: "posts" }} onNavigate={vi.fn()} />);
    expect(await screen.findByRole("alert")).toBeTruthy();
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Post 1")).toBeTruthy();
  });

  it("shows more with the cursor the server gave, and appends", async () => {
    const calls = server({ list: (u) => (u.searchParams.get("after") ? { items: [row(2)], next: "" } : { items: [row(1)], next: "2026-11-08|abc" }) });
    render(<AdminScreens route={{ kind: "posts" }} onNavigate={vi.fn()} />);
    await screen.findByText("Post 1");
    fireEvent.click(screen.getByRole("button", { name: "Show more" }));
    expect(await screen.findByText("Post 2")).toBeTruthy();
    expect(screen.getByText("Post 1")).toBeTruthy();
    expect(calls.some((c) => c.path.includes("after=2026-11-08%7Cabc"))).toBe(true);
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
  });
});

describe("Admin → one post", () => {
  it("shows the published content and takes the post down only with a reason", async () => {
    const calls = server();
    render(<AdminScreens route={{ kind: "post", id: ID(1) }} onNavigate={vi.fn()} />);
    expect(await screen.findByText("The published body")).toBeTruthy();
    expect(screen.getByText(/as visitors see it/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Take down" }));
    const form = screen.getByRole("form", { name: "Confirm: Take down" });
    expect(form.textContent).toContain("Copies already saved elsewhere cannot be removed");
    fireEvent.click(screen.getByRole("button", { name: "Confirm take down" }));
    expect(await screen.findByText("Write a reason. The author needs to know why.")).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    fireEvent.change(screen.getByLabelText("Reason (required, shown to the author)"), { target: { value: "  Spam  " } });
    expect(screen.getByText("8 / 500")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Confirm take down" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ reason: "Spam" }));
  });

  it("restores a taken-down post, and tells the administrator when the author had unpublished it", async () => {
    server({ one: view({ state: "taken_down", takedown: { at: "2026-11-09T00:00:00Z", reason: "Spam", byName: "Boss" } }), restore: { note: "The author has unpublished it; it stays unpublished." } });
    render(<AdminScreens route={{ kind: "post", id: ID(1) }} onNavigate={vi.fn()} />);
    expect(await screen.findByText(/Reason shown to the author: Spam/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Restore" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm restore" }));
    expect(await screen.findByText("The author has unpublished it; it stays unpublished.")).toBeTruthy();
  });

  it("says plainly when someone already took it down, changing nothing", async () => {
    server({ down: 409 });
    render(<AdminScreens route={{ kind: "post", id: ID(1) }} onNavigate={vi.fn()} />);
    await screen.findByText("The published body");
    fireEvent.click(screen.getByRole("button", { name: "Take down" }));
    fireEvent.change(screen.getByLabelText("Reason (required, shown to the author)"), { target: { value: "Spam" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm take down" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Someone already took this post down");
  });

  it("explains a post whose author unpublished it after the takedown, and shows no content for it", async () => {
    server({ one: view({ state: "taken_down", doc: undefined, takedown: { at: "2026-11-09T00:00:00Z", reason: "Spam", byName: "Boss" } }) });
    render(<AdminScreens route={{ kind: "post", id: ID(1) }} onNavigate={vi.fn()} />);
    expect(await screen.findByText(/no published version to show/)).toBeTruthy();
    expect(screen.queryByText("The published body")).toBeNull();
  });
});

describe("the admin post readers", () => {
  it("refuse an unknown state or a malformed id rather than guess", () => {
    expect(parseAdminPost(row(1, { state: "deleted" }))).toBeNull();
    expect(parseAdminPost({ ...row(1), id: "x" })).toBeNull();
    expect(parseAdminPostPage({ items: [row(1), { nonsense: true }], next: "" })).toBeNull();
  });
  it("keep only the fields the screens use", () => {
    const p = parseAdminPost({ ...row(1), secret: "x", draft: "LEAK", email: "a@b.test" });
    expect(Object.keys(p!).sort()).toEqual(["authorName", "id", "publishedAt", "slug", "state", "summary", "takedown", "title"]);
  });
  it("route /admin/posts and /admin/posts/{id}, and nothing else under it", () => {
    expect(parseAdminPath("/admin/posts")).toEqual({ kind: "posts" });
    expect(parseAdminPath(`/admin/posts/${ID(1)}`)).toEqual({ kind: "post", id: ID(1) });
    expect(parseAdminPath("/admin/posts/nope")).toEqual({ kind: "unknown" });
  });
});

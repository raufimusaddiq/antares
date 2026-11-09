// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PostList } from "./PostList";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const post = (over: Record<string, unknown>) => ({ id: "p0", title: "A post", summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc: { type: "doc", content: [] }, check: null, updatedAt: "2026-11-08T10:00:00Z", publishedAt: "", interactive: false, ...over });
const FIVE = [
  post({ id: "d1", title: "Draft one", status: "draft" }),
  post({ id: "c1", title: "Checking one", status: "checking" }),
  post({ id: "r1", title: "Ready one", status: "ready" }),
  post({ id: "pub1", title: "Published one", status: "published", slug: "published-one", publishedAt: "2026-11-08T10:00:00Z", interactive: true }),
  post({ id: "u1", title: "Unpublished one", status: "unpublished", slug: "was-public" }),
];

type Call = { method: string; url: string };
/** A fake server: the list answers with `posts` (a function, so a test can change it), anything else with `other`. */
function serve(posts: () => unknown, other: (c: Call) => Response = () => json(post({}))) {
  const calls: Call[] = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const c = { method: init?.method ?? "GET", url: String(url) };
    calls.push(c);
    return c.method === "GET" && c.url === "/api/blog/posts" ? (typeof posts() === "number" ? json({}, posts() as number) : json(posts())) : other(c);
  });
  return calls;
}
const mount = (onOpen = vi.fn()) => ({ onOpen, ...render(<PostList onOpen={onOpen} onChats={() => {}} />) });
const row = (title: string) => screen.getByText(title).closest("li") as HTMLElement;

describe("My posts: states", () => {
  it("says it is loading until the server answers", async () => {
    let release: (r: Response) => void = () => {};
    vi.stubGlobal("fetch", () => new Promise<Response>((r) => (release = r)));
    mount();
    expect(screen.getByText(/loading your posts/i).getAttribute("aria-busy")).toBe("true");
    release(json([]));
    await screen.findByText("No posts yet");
    expect(screen.queryByText(/loading your posts/i)).toBeNull();
  });

  it("invites a first post when there are none, and opens the new draft", async () => {
    const calls = serve(() => [], () => json(post({ id: "new1" })));
    const { onOpen } = mount();
    fireEvent.click(await screen.findByRole("button", { name: "Write your first post" }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith("new1"));
    expect(calls.filter((c) => c.method === "POST")).toEqual([{ method: "POST", url: "/api/blog/posts" }]);
  });

  it("says what went wrong and tries again on request", async () => {
    let up = false;
    serve(() => (up ? [post({ id: "d1", title: "Back again" })] : 500));
    mount();
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load your posts/i);
    up = true;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("Back again");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows a status chip in the words of the design for each of the five states", async () => {
    serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    const chips = (t: string) => within(row(t)).getByText(/^(Draft|Checking|Ready to publish|Published|Unpublished)$/).textContent;
    expect(chips("Draft one")).toBe("Draft");
    expect(chips("Checking one")).toBe("Checking");
    expect(chips("Ready one")).toBe("Ready to publish");
    expect(chips("Published one")).toBe("Published");
    expect(chips("Unpublished one")).toBe("Unpublished");
    expect(within(row("Published one")).getByText("Interactive")).toBeTruthy();
    expect(within(row("Draft one")).queryByText("Interactive")).toBeNull();
  });

  it("calls an untitled post Untitled", async () => {
    serve(() => [post({ id: "x", title: "" })]);
    mount();
    expect(await screen.findByText("Untitled")).toBeTruthy();
  });
});

describe("My posts: row actions", () => {
  it("offers View and Unpublish only on a published post, and View goes to its address", async () => {
    serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    const pub = within(row("Published one"));
    expect(pub.getByRole("link", { name: "View" }).getAttribute("href")).toBe("/b/published-one");
    expect(pub.getByRole("button", { name: "Unpublish" })).toBeTruthy();
    for (const t of ["Draft one", "Checking one", "Ready one", "Unpublished one"]) {
      expect(within(row(t)).queryByRole("link", { name: "View" })).toBeNull();
      expect(within(row(t)).queryByRole("button", { name: "Unpublish" })).toBeNull();
      expect(within(row(t)).getByRole("button", { name: "Duplicate" })).toBeTruthy();
      expect(within(row(t)).getByRole("button", { name: "Delete" })).toBeTruthy();
    }
  });

  it("opens a post from its title", async () => {
    serve(() => FIVE);
    const { onOpen } = mount();
    fireEvent.click((await screen.findByText("Ready one")).closest("button") as HTMLElement);
    expect(onOpen).toHaveBeenCalledWith("r1");
  });

  it("unpublishes and duplicates through the right routes, then reloads the list", async () => {
    const calls = serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(within(row("Published one")).getByRole("button", { name: "Unpublish" }));
    await waitFor(() => expect(calls).toContainEqual({ method: "POST", url: "/api/blog/posts/pub1/unpublish" }));
    fireEvent.click(within(row("Draft one")).getByRole("button", { name: "Duplicate" }));
    await waitFor(() => expect(calls).toContainEqual({ method: "POST", url: "/api/blog/posts/d1/duplicate" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "GET").length).toBeGreaterThanOrEqual(3));
  });

  it("shows why an action failed and keeps the list", async () => {
    serve(() => FIVE, () => json({ error: { code: "CONFLICT" } }, 409));
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(within(row("Draft one")).getByRole("button", { name: "Duplicate" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/changed somewhere else/i);
    expect(screen.getByText("Draft one")).toBeTruthy();
  });
});

describe("My posts: delete confirmation", () => {
  it("asks first, and nothing is deleted until the author says so", async () => {
    const calls = serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(within(row("Draft one")).getByRole("button", { name: "Delete" }));
    const dlg = screen.getByRole("alertdialog", { name: "Delete post" });
    expect(dlg.textContent).toContain("Delete “Draft one”?");
    expect(dlg.textContent).toMatch(/cannot be undone/i);
    expect(dlg.textContent).not.toMatch(/public now/i);
    fireEvent.click(within(dlg).getByRole("button", { name: "Keep it" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  it("warns that a published post stops being readable", async () => {
    serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(within(row("Published one")).getByRole("button", { name: "Delete" }));
    expect(screen.getByRole("alertdialog").textContent).toMatch(/public now and will stop being readable/i);
  });

  it("deletes the post it was opened for, then reloads the list", async () => {
    let posts = FIVE;
    const calls = serve(() => posts, (c) => {
      if (c.method === "DELETE") posts = posts.filter((p) => p.id !== "d1");
      return json({});
    });
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(within(row("Draft one")).getByRole("button", { name: "Delete" }));
    fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete post" }));
    await waitFor(() => expect(calls).toContainEqual({ method: "DELETE", url: "/api/blog/posts/d1" }));
    await waitFor(() => expect(screen.queryByText("Draft one")).toBeNull());
    expect(screen.getByText("Checking one")).toBeTruthy();
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });
});

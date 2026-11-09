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
const mount = (onOpen = vi.fn(), onChats = vi.fn()) => ({ onOpen, onChats, ...render(<PostList email="writer@example.test" onOpen={onOpen} onChats={onChats} />) });
/** Open the "⋯" menu of a row. */
const menuOf = (title: string) => {
  fireEvent.click(within(row(title)).getByRole("button", { name: `More actions for ${title}` }));
  return within(screen.getByRole("menu"));
};
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

describe("My posts: the rail", () => {
  it("has the design's rail: brand, New post, Chats, Blog posts as the current page, and the account", async () => {
    serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    const rail = screen.getByRole("navigation", { name: "Antares" });
    expect(within(rail).getByText("Antares")).toBeTruthy();
    expect(within(rail).getByRole("button", { name: "New post" })).toBeTruthy();
    expect(within(rail).getByRole("button", { name: "Chats" })).toBeTruthy();
    expect(within(rail).getByRole("button", { name: "Blog posts" }).getAttribute("aria-current")).toBe("page");
    expect(within(rail).getByText("writer@example.test")).toBeTruthy();
  });

  it("goes back to the chats, and starts a post from the rail as from the page", async () => {
    const calls = serve(() => FIVE, () => json(post({ id: "new1" })));
    const { onChats, onOpen } = mount();
    await screen.findByText("Draft one");
    fireEvent.click(within(screen.getByRole("navigation", { name: "Antares" })).getByRole("button", { name: "Chats" }));
    expect(onChats).toHaveBeenCalledTimes(1);
    fireEvent.click(within(screen.getByRole("navigation", { name: "Antares" })).getByRole("button", { name: "New post" }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith("new1"));
    expect(calls.filter((c) => c.method === "POST")).toEqual([{ method: "POST", url: "/api/blog/posts" }]);
  });
});

describe("My posts: row actions", () => {
  it("offers View post and Unpublish only on a published post, and View goes to its address", async () => {
    serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    const pub = menuOf("Published one");
    expect(pub.getAllByRole("menuitem").map((m) => m.textContent)).toEqual(["Open", "View post", "Duplicate", "Unpublish", "Delete"]);
    expect(pub.getByRole("menuitem", { name: "View post" }).getAttribute("href")).toBe("/b/published-one");
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    for (const t of ["Draft one", "Checking one", "Ready one", "Unpublished one"]) {
      const m = menuOf(t);
      expect(m.getAllByRole("menuitem").map((x) => x.textContent)).toEqual(["Open", "Duplicate", "Delete"]);
      fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    }
  });

  it("opens a post with its Open button, or from the menu", async () => {
    serve(() => FIVE);
    const { onOpen } = mount();
    await screen.findByText("Ready one");
    fireEvent.click(within(row("Ready one")).getByRole("button", { name: "Open Ready one" }));
    expect(onOpen).toHaveBeenCalledWith("r1");
    fireEvent.click(menuOf("Draft one").getByRole("menuitem", { name: "Open" }));
    expect(onOpen).toHaveBeenCalledWith("d1");
    expect(screen.queryByRole("menu")).toBeNull(); // choosing closes the menu
  });

  it("unpublishes and duplicates through the right routes, then reloads the list", async () => {
    const calls = serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(menuOf("Published one").getByRole("menuitem", { name: "Unpublish" }));
    await waitFor(() => expect(calls).toContainEqual({ method: "POST", url: "/api/blog/posts/pub1/unpublish" }));
    fireEvent.click(menuOf("Draft one").getByRole("menuitem", { name: "Duplicate" }));
    await waitFor(() => expect(calls).toContainEqual({ method: "POST", url: "/api/blog/posts/d1/duplicate" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "GET").length).toBeGreaterThanOrEqual(3));
  });

  it("shows why an action failed and keeps the list", async () => {
    serve(() => FIVE, () => json({ error: { code: "CONFLICT" } }, 409));
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(menuOf("Draft one").getByRole("menuitem", { name: "Duplicate" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/changed somewhere else/i);
    expect(screen.getByText("Draft one")).toBeTruthy();
  });
});

describe("My posts: delete confirmation", () => {
  it("asks first, and nothing is deleted until the author says so", async () => {
    const calls = serve(() => FIVE);
    mount();
    await screen.findByText("Draft one");
    fireEvent.click(menuOf("Draft one").getByRole("menuitem", { name: "Delete" }));
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
    fireEvent.click(menuOf("Published one").getByRole("menuitem", { name: "Delete" }));
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
    fireEvent.click(menuOf("Draft one").getByRole("menuitem", { name: "Delete" }));
    fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete post" }));
    await waitFor(() => expect(calls).toContainEqual({ method: "DELETE", url: "/api/blog/posts/d1" }));
    await waitFor(() => expect(screen.queryByText("Draft one")).toBeNull());
    expect(screen.getByText("Checking one")).toBeTruthy();
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });
});

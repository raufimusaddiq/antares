// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { describeBlogError, parsePost, parseSummary } from "./blogApi";
import { PostList } from "./PostList";
import { TakedownNotices } from "./TakedownNotice";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const NOTICE = { postId: "p1", title: "Flights", reason: "Advertising a booking site", at: "2026-11-09T00:00:00Z" };

function server(notices: unknown[] | number, posts: unknown[] = []) {
  const calls: { method: string; path: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = new URL(String(input), "https://antares.test");
      calls.push({ method: init?.method ?? "GET", path: u.pathname });
      if (u.pathname === "/api/blog/notices") return typeof notices === "number" ? json(notices, {}) : json(200, { notices });
      if (u.pathname.endsWith("/seen")) return new Response(null, { status: 204 });
      if (u.pathname === "/api/blog/posts") return json(200, posts);
      return json(404, {});
    }),
  );
  return calls;
}

describe("the one-time takedown banner", () => {
  it("names the post and the reason, and marks it seen when dismissed", async () => {
    const calls = server([NOTICE]);
    render(<TakedownNotices />);
    expect(await screen.findByText(/took down your post “Flights”/)).toBeTruthy();
    expect(screen.getByText(/Reason: Advertising a booking site/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Got it" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.path === "/api/blog/notices/p1/seen")).toBe(true));
    expect(screen.queryByText(/took down/)).toBeNull();
  });
  it("says how many when there are several, and marks each one", async () => {
    const calls = server([NOTICE, { ...NOTICE, postId: "p2", title: "Second" }]);
    render(<TakedownNotices />);
    expect(await screen.findByText(/took down 2 of your posts/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Got it" }));
    await waitFor(() => expect(calls.filter((c) => c.path.endsWith("/seen")).length).toBe(2));
  });
  it("offers My posts when there is somewhere to go, and goes there", async () => {
    server([NOTICE]);
    const open = vi.fn();
    render(<TakedownNotices onOpen={open} />);
    fireEvent.click(await screen.findByRole("button", { name: "See my posts" }));
    expect(open).toHaveBeenCalled();
  });
  it.each([["nothing to say", [] as unknown[]], ["a failed read", 500]])("shows nothing for %s", async (_n, notices) => {
    const calls = server(notices as unknown[] | number);
    const { container } = render(<TakedownNotices />);
    await waitFor(() => expect(calls.length).toBe(1));
    expect(container.textContent).toBe("");
  });
});

describe("a taken-down post in the author's own list", () => {
  const down = { id: "p1", title: "Flights", status: "published", slug: "flights", updatedAt: "2026-11-08T00:00:00Z", publishedAt: "2026-11-08T00:00:00Z", interactive: false, takedown: { at: "2026-11-09T00:00:00Z", reason: "Advertising a booking site" } };
  it("shows Taken down and the reason, and is not offered as a public address or View post", async () => {
    server([], [down]);
    render(<PostList email="w@example.test" onOpen={vi.fn()} onChats={vi.fn()} />);
    expect(await screen.findByText("Taken down")).toBeTruthy();
    expect(screen.getByText(/took it down: Advertising a booking site/)).toBeTruthy();
    expect(screen.queryByText(/\/b\/flights/)).toBeNull();
    expect(screen.queryByText("Published")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More actions for Flights" }));
    expect(screen.queryByRole("menuitem", { name: "View post" })).toBeNull();
  });
  it("is read from the server's answer, and never carries who", () => {
    const s = parseSummary(down);
    expect(s?.takedown).toEqual({ at: "2026-11-09T00:00:00Z", reason: "Advertising a booking site" });
    const p = parsePost({ ...down, summary: "", coverUrl: "", latestVersion: 1, publishedVersion: 1, doc: { type: "doc" }, check: null, takedown: { at: "x", reason: "y", by: "boss@example.test" } });
    expect(JSON.stringify(p)).not.toContain("boss");
  });
  it("is explained when a publish is refused for it", () => {
    expect(describeBlogError({ status: 409, code: "TAKEN_DOWN" })).toContain("cannot be published until it is restored");
    expect(describeBlogError({ status: 409, code: "CONFLICT" })).toBe("This post changed somewhere else.");
  });
});

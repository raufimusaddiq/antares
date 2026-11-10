// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PublicIndex, PublicPostPage } from "./Public";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const reply = (status: number, body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } }));
const POST = { title: "A post", summary: "s", coverUrl: "", doc: { type: "doc", content: [] }, blocks: [], authorName: "A Writer", publishedAt: "2026-11-08T01:00:00Z" };

// Spec 002 FR-011: Share is offered on a published post and nowhere else a reader can be.
describe("where Share appears", () => {
  it("on a published post", async () => {
    vi.stubGlobal("fetch", reply(200, POST));
    render(<PublicPostPage slug="a-post" />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Share this post" })).toBeTruthy());
  });
  it("not on a post that is not found, one that failed to load, or the index", async () => {
    vi.stubGlobal("fetch", reply(404, {}));
    const gone = render(<PublicPostPage slug="a-post" />);
    await waitFor(() => expect(screen.getByText("This post is not available")).toBeTruthy());
    expect(screen.queryByRole("button", { name: "Share this post" })).toBeNull();
    gone.unmount();
    vi.stubGlobal("fetch", reply(500, {}));
    const bad = render(<PublicPostPage slug="a-post" />);
    await waitFor(() => expect(screen.getByText("We could not load this post")).toBeTruthy());
    expect(screen.queryByRole("button", { name: "Share this post" })).toBeNull();
    bad.unmount();
    vi.stubGlobal("fetch", reply(200, []));
    render(<PublicIndex />);
    await waitFor(() => expect(screen.queryByText("Loading posts…")).toBeNull());
    expect(screen.queryByRole("button", { name: "Share this post" })).toBeNull();
  });
});

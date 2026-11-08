// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { isBlogPath, parseBlogPath } from "./BlogApp";
import { httpsOnly, mediaUrl, parseCheck, parsePublicPost } from "./blogApi";
import { DocView, safeHref } from "./DocView";
import { publicBlogRoute } from "./Public";
import { PublishFlow, failureWords } from "./PublishFlow";
import { EMPTY_DOC, type Check, type Post } from "./types";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("image addresses", () => {
  it("keeps this site's own paths, blobs and https; drops every other scheme", () => {
    expect(mediaUrl("/api/blog/posts/p1/assets/a.png")).toBe("/api/blog/posts/p1/assets/a.png");
    expect(mediaUrl("blob:https://x/1")).toBe("blob:https://x/1");
    expect(mediaUrl("https://example.org/a.png")).not.toBe("");
    for (const bad of ["//evil.example/a.png", "http://example.org/a.png", "javascript:alert(1)", "data:image/svg+xml,x", "/\\evil", 5, undefined]) expect(mediaUrl(bad)).toBe("");
  });
});

describe("what a reader can be shown", () => {
  it("draws text, never markup: script, raw html and unknown nodes stay inert", () => {
    const doc = {
      type: "doc",
      content: [
        { type: "paragraph", content: [{ type: "text", text: "<img src=x onerror=alert(1)>" }] },
        { type: "script", content: [{ type: "text", text: "alert(1)" }] },
        { type: "paragraph", content: [{ type: "text", text: "click", marks: [{ type: "link", attrs: { href: "javascript:alert(1)" } }] }] },
      ],
    };
    const { container } = render(<DocView doc={doc} />);
    expect(container.querySelector("img, script, a")).toBeNull();
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(container.textContent).not.toContain("alert(1)</");
    expect(safeHref("javascript:alert(1)")).toBe("");
    expect(safeHref("data:text/html,x")).toBe("");
    expect(safeHref("https://example.org/a")).not.toBe("");
  });

  it("a link opens with no opener and no referrer", () => {
    const doc = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "a", marks: [{ type: "link", attrs: { href: "https://example.org" } }] }] }] };
    const { container } = render(<DocView doc={doc} />);
    const a = container.querySelector("a")!;
    expect(a.getAttribute("rel")).toContain("noopener");
    expect(a.getAttribute("rel")).toContain("noreferrer");
  });

  it("keeps only https sources without credentials", () => {
    expect(httpsOnly("https://example.org/x")).not.toBe("");
    for (const bad of ["http://example.org", "https://u:p@example.org", "javascript:1", "ftp://x", "not a url", 5]) expect(httpsOnly(bad)).toBe("");
    const p = parsePublicPost({ title: "T", doc: EMPTY_DOC, blocks: [{ anchor: 0, markup: "", fallback: "f", sources: [{ url: "http://x.org", title: "x" }, { url: "https://y.org", title: "y" }] }] });
    expect(p!.blocks[0].sources.map((s) => s.url)).toEqual(["https://y.org/"]);
  });

  it("reads no author identity: the public post type carries only a display name", () => {
    const p = parsePublicPost({ title: "T", doc: EMPTY_DOC, authorName: "Maya", authorId: "u1", email: "m@x.org", profile: "p" });
    expect(Object.keys(p!).sort()).toEqual(["authorName", "blocks", "coverUrl", "doc", "publishedAt", "summary", "title"]);
  });
});

describe("addresses", () => {
  it("only /b and /b/<slug> are public blog pages", () => {
    expect(publicBlogRoute("/b")).toEqual({ kind: "index" });
    expect(publicBlogRoute("/b/")).toEqual({ kind: "index" });
    expect(publicBlogRoute("/b/my-post-1")).toEqual({ kind: "post", slug: "my-post-1" });
    for (const p of ["/blog", "/b/UPPER", "/b/a", "/b/a/b", "/b/../admin", "/"]) expect(publicBlogRoute(p)).toBeNull();
  });
  it("the author area is /blog, /blog/<id> and /blog/<id>/publish", () => {
    expect(isBlogPath("/blog")).toBe(true);
    expect(isBlogPath("/b")).toBe(false);
    expect(parseBlogPath("/blog")).toEqual({ kind: "list" });
    expect(parseBlogPath("/blog/abc123xyz")).toEqual({ kind: "edit", id: "abc123xyz" });
    expect(parseBlogPath("/blog/abc123xyz/publish")).toEqual({ kind: "publish", id: "abc123xyz" });
    expect(parseBlogPath("/blog/x/y/z")).toEqual({ kind: "list" });
  });
});

const check = (over: Partial<Check>): Check => ({ id: "c1", state: "ready", reason: "", notes: [], notices: [], blocks: [], ...over });
const post = (c: Check | null): Post => ({ id: "p1", title: "Title", summary: "", slug: "title", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc: EMPTY_DOC, check: c, updatedAt: "" });
const flow = (c: Check | null, caps = { writing: true, check: true, webSearch: true }) =>
  render(<PublishFlow post={post(c)} caps={caps} flush={async () => true} onPost={() => {}} onBackToEditing={() => {}} onMyPosts={() => {}} />);

describe("publish flow", () => {
  it("every way a check can stop is shown as itself, never as completed", () => {
    expect(failureWords(check({ state: "failed", reason: "error" }))).toMatch(/reported an error/);
    expect(failureWords(check({ state: "failed", reason: "disconnected" }))).toMatch(/lost/);
    expect(failureWords(check({ state: "failed", reason: "unusable" }))).toMatch(/could not be used/);
    expect(failureWords(check({ state: "timed_out" }))).toMatch(/timed out/);
    expect(failureWords(check({ state: "cancelled" }))).toMatch(/Cancelled/);
    expect(failureWords(check({ state: "out_of_date" }))).toMatch(/changed/);
  });

  it("a failed check offers retry, publish original and back, and publishes nothing", () => {
    flow(check({ state: "timed_out" }));
    expect(screen.getByRole("alert").textContent).toMatch(/timed out/);
    expect(screen.getByText("Nothing was published.")).toBeTruthy();
    for (const name of ["Try again", "Publish original", "Back to editing"]) expect(screen.getByRole("button", { name })).toBeTruthy();
  });

  it("without the checking capability only the original can be published, and it says so", () => {
    flow(null, { writing: true, check: false, webSearch: false });
    expect(screen.queryByRole("button", { name: "Check and add blocks" })).toBeNull();
    expect(screen.getByRole("status").textContent).toMatch(/not available/);
    expect(screen.getByRole("button", { name: "Publish my original" })).toBeTruthy();
  });

  it("a block with an unverified source cannot be accepted until the author acknowledges it", async () => {
    const src = { url: "https://example.org/x", title: "X", retrievedAt: "2026-11-08", verified: false };
    const block = { id: "b1", kind: "table", afterNode: 0, markup: "", fallback: "plain", decision: "proposed" as const, needsAck: true, sources: [src] };
    flow(check({ blocks: [block] }));
    const accept = await waitFor(() => screen.getByRole("button", { name: "Accept" }));
    expect((accept as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("unverified")).toBeTruthy();
    fireEvent.click(screen.getByLabelText(/checked these sources/));
    expect((accept as HTMLButtonElement).disabled).toBe(false);
  });

  it("parses a check strictly: an unknown state is not a check", () => {
    expect(parseCheck({ id: "c", state: "weird" })).toBeNull();
    expect(parseCheck({ id: "c", state: "ready", blocks: [{ id: "b", decision: "maybe" }] })!.blocks[0].decision).toBe("proposed");
  });
});

describe("images in a published post", () => {
  it("are fetched from the public route, not the author-only one", async () => {
    const { publicImageUrl } = await import("./Public");
    const f = publicImageUrl("my-post");
    expect(f("/api/blog/posts/p1abcdef/assets/a1b2c3")).toBe("/api/public/blog/posts/my-post/assets/a1b2c3");
    expect(f("https://example.org/x.png")).toBe("https://example.org/x.png");
  });
});

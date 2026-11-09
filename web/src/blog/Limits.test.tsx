// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parseLimits } from "./blogApi";
import { checkBytes, imageCount, sizeWords } from "./limits";
import { PublishFlow } from "./PublishFlow";
import type { DocNode, Post } from "./types";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const p = (text: string): DocNode => ({ type: "paragraph", content: [{ type: "text", text }] });
const doc = (...content: DocNode[]): DocNode => ({ type: "doc", content });
const enc = (s: string) => new TextEncoder().encode(s).length;

describe("what a check would read", () => {
  it("is the title and each top-level part, numbered, as the server builds it", () => {
    expect(checkBytes("  Hello ", doc(p("one"), p("two  ")))).toBe(enc("TITLE: Hello\n\n[0] one\n\n[1] two\n\n"));
  });
  it("counts bytes, not characters", () => {
    expect(checkBytes("é", doc(p("日本")))).toBe(enc("TITLE: é\n\n[0] 日本\n\n"));
  });
  it("spells headings, lists, breaks, rules and images as the server does", () => {
    const d = doc(
      { type: "heading", attrs: { level: 2 }, content: [{ type: "text", text: "Head" }] },
      { type: "bulletList", content: [{ type: "listItem", content: [p("a")] }, { type: "listItem", content: [p("b")] }] },
      { type: "paragraph", content: [{ type: "text", text: "x" }, { type: "hardBreak" }, { type: "text", text: "y" }] },
      { type: "horizontalRule" },
      { type: "image", attrs: { src: "/u", alt: "A cat" } },
      { type: "image", attrs: { src: "/u" } },
    );
    expect(checkBytes("T", d)).toBe(enc("TITLE: T\n\n[0] ## Head\n\n[1] - a\n- b\n\n[2] x\ny\n\n[3] ---\n\n[4] (image: A cat)\n\n[5] (image)\n\n"));
  });
  it("counts images wherever they are", () => {
    expect(imageCount(doc(p("x"), { type: "image" }, { type: "blockquote", content: [{ type: "image" }, p("y")] }))).toBe(2);
    expect(imageCount(doc(p("x")))).toBe(0);
  });
});

describe("sizes in words", () => {
  it("reads as the author expects", () => {
    expect(sizeWords(120 << 10)).toBe("120 KB");
    expect(sizeWords(10)).toBe("1 KB");
    expect(sizeWords(5 << 20)).toBe("5 MB");
    expect(sizeWords(1.5 * (1 << 20))).toBe("1.5 MB");
  });
});

describe("the limits reply", () => {
  const full = { checkBytes: 122880, docBytes: 524288, imagesPerPost: 30, imageBytes: 5242880, imageBytesTotal: 209715200, blocksPerCheck: 10, sourcesPerBlock: 10, checksPer24h: 30 };
  it("is read whole", () => expect(parseLimits(full)).toEqual(full));
  it("is not guessed at when a number is missing: no limits is better than limits of zero", () => {
    expect(parseLimits({ ...full, imagesPerPost: undefined })).toBeNull();
    expect(parseLimits({ ...full, checkBytes: 0 })).toBeNull();
    expect(parseLimits("x")).toBeNull();
  });
});

const LIMITS = { checkBytes: 200, docBytes: 524288, imagesPerPost: 30, imageBytes: 5242880, imageBytesTotal: 209715200, blocksPerCheck: 10, sourcesPerBlock: 10, checksPer24h: 30 };
const post = (d: DocNode, title = "A title"): Post => ({ id: "p1", title, summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc: d, check: null, updatedAt: "" }) as Post;
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json" } });
const flow = (d: DocNode, caps = { writing: true, check: true, webSearch: true }) =>
  render(<PublishFlow post={post(d)} caps={caps} flush={async () => true} onPost={() => {}} onBackToEditing={() => {}} onMyPosts={() => {}} />);

describe("Publish: the limits are stated before anyone publishes", () => {
  it("shows what a check reads, what it proposes, and the image allowance, with this post's numbers", async () => {
    vi.stubGlobal("fetch", async () => json(LIMITS));
    flow(doc(p("short"), { type: "image" }));
    const box = await screen.findByRole("region", { name: "Limits" });
    expect(box.textContent).toMatch(/reads up to 1 KB of text/);
    expect(box.textContent).toMatch(/This post is about 1 KB/);
    expect(box.textContent).toMatch(/up to 10 blocks, each naming up to 10 sources/);
    expect(box.textContent).toMatch(/30 checks in 24 hours/);
    expect(box.textContent).toMatch(/Images: 1 of 30 in this post, up to 5 MB each, and up to 200 MB across all your posts/);
    expect((screen.getByRole("button", { name: "Check and enrich" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("says a post is too long to check before it is tried, and still lets the original go out", async () => {
    vi.stubGlobal("fetch", async () => json(LIMITS));
    flow(doc(p("word ".repeat(100))));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/longer than the assistant can read in one check/);
    expect(alert.textContent).toMatch(/Publish your original, or shorten it/);
    expect((screen.getByRole("button", { name: "Check and enrich" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Publish my original" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("does not talk about checks when checking is not available", async () => {
    vi.stubGlobal("fetch", async () => json(LIMITS));
    flow(doc(p("x")), { writing: true, check: false, webSearch: false });
    const box = await screen.findByRole("region", { name: "Limits" });
    expect(box.textContent).not.toMatch(/check/i);
    expect(box.textContent).toMatch(/Images:/);
  });

  it("shows nothing, and blocks nothing, when the limits cannot be had", async () => {
    vi.stubGlobal("fetch", async () => json({}, 500));
    flow(doc(p("x")));
    await waitFor(() => expect(screen.getByRole("button", { name: "Check and enrich" })).toBeTruthy());
    expect(screen.queryByRole("region", { name: "Limits" })).toBeNull();
    expect((screen.getByRole("button", { name: "Check and enrich" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("explains a refusal from the server in the same words", async () => {
    vi.stubGlobal("fetch", async (_u: string, init?: RequestInit) => (init?.method === "POST" ? json({ error: { code: "TOO_LONG_TO_CHECK" } }, 422) : json(LIMITS)));
    flow(doc(p("short")));
    await screen.findByRole("region", { name: "Limits" });
    fireEvent.click(screen.getByRole("button", { name: "Check and enrich" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/longer than the assistant can read in one check/);
  });
});

// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PublishFlow } from "./PublishFlow";
import type { Check, CheckBlock, DocNode, Post } from "./types";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json" } });
const doc: DocNode = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "My own words, unchanged." }] }] };
const src = (host: string) => ({ url: `https://${host}/page`, title: `About ${host}`, retrievedAt: "2026-11-08T10:00:00Z", verified: false });
const block = (id: string, over: Partial<CheckBlock> = {}): CheckBlock => ({ id, kind: "table", afterNode: 0, markup: "", fallback: `plain ${id}`, decision: "proposed", needsAck: true, sources: [src(`${id}.example.org`)], ...over });
const check = (over: Partial<Check> = {}): Check => ({ id: "c1", state: "ready", reason: "", notes: [], notices: [], blocks: [block("b1"), block("b2")], ...over });
const post = (c: Check): Post => ({ id: "p1", title: "My post", summary: "", slug: "my-post", coverUrl: "", status: "ready", latestVersion: 2, publishedVersion: 0, doc, check: c, updatedAt: "" }) as Post;

type Call = { method: string; url: string; body: unknown };
function serve(over: (c: Call) => Response | null = () => null) {
  const calls: Call[] = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const c = { method: init?.method ?? "GET", url: String(url), body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(c);
    const o = over(c);
    if (o) return o;
    const m = /\/blocks\/(\w+)$/.exec(c.url);
    if (c.method === "PUT" && m) return json(block(m[1], { decision: (c.body as { decision: CheckBlock["decision"] }).decision }));
    if (c.url.endsWith("/publish")) return json({ ...post(check()), status: "published" });
    return json({}, 500);
  });
  return calls;
}
const mount = (c: Check, onBack = vi.fn()) => {
  render(<PublishFlow post={post(c)} caps={{ writing: true, check: true, webSearch: true }} flush={async () => true} onPost={() => {}} onBackToEditing={onBack} onMyPosts={() => {}} />);
  return onBack;
};
const blockEl = (n: number) => screen.getAllByRole("article")[n];

describe("Review before publishing", () => {
  it("is the design's three parts: the original, the enriched preview, the assistant's notes", () => {
    serve();
    mount(check({ notes: [{ kind: "clarity", text: "Lead with the question.", afterNode: 0 }, { kind: "unsourced", text: "No source for this claim.", afterNode: 0 }] }));
    expect(screen.getByRole("heading", { name: "Review before publishing" })).toBeTruthy();
    expect(screen.getByText("Everything marked AI-added is a proposal. Nothing is public until you publish.")).toBeTruthy();
    const original = screen.getByRole("region", { name: "Your original" });
    expect(within(original).getByRole("heading", { name: "My post" })).toBeTruthy();
    expect(within(original).getByText("My own words, unchanged.")).toBeTruthy();
    const enriched = screen.getByRole("region", { name: "Enriched preview" });
    expect(within(enriched).getAllByText("AI-added")).toHaveLength(2);
    const notes = screen.getByRole("region", { name: "Notes from the assistant" });
    expect(within(notes).getByText("Clarity")).toBeTruthy();
    expect(within(notes).getByText("Unsourced")).toBeTruthy();
    expect(within(notes).getByText("No source for this claim.").closest(".blog-note")!.className).toContain("blog-note-warn");
    expect(within(notes).getByText("Lead with the question.").closest(".blog-note")!.className).not.toContain("warn");
  });

  it("offers Publish my original and Accept all and publish, and a way back to editing", () => {
    serve();
    const onBack = mount(check());
    expect(screen.getByRole("button", { name: "Publish my original" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Accept all and publish" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "‹ Back to editing" }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it("takes focus to its heading when it opens", () => {
    serve();
    mount(check());
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Review before publishing" }));
  });
});

describe("Accept all and publish", () => {
  it("waits for the tick on every block, and says how many are missing", () => {
    serve();
    mount(check());
    const all = screen.getByRole("button", { name: "Accept all and publish" }) as HTMLButtonElement;
    expect(all.disabled).toBe(true);
    expect(screen.getByText(/on 2 blocks before accepting all/)).toBeTruthy();
    expect(all.getAttribute("aria-describedby")).toBe("accept-all-hint");
    fireEvent.click(within(blockEl(0)).getByRole("checkbox"));
    expect(screen.getByText(/on 1 block before accepting all/)).toBeTruthy();
    fireEvent.click(within(blockEl(1)).getByRole("checkbox"));
    expect(all.disabled).toBe(false);
    expect(screen.queryByText(/before accepting all/)).toBeNull();
  });

  it("accepts every block with its acknowledgement and then publishes with them", async () => {
    const calls = serve();
    mount(check());
    for (const n of [0, 1]) fireEvent.click(within(blockEl(n)).getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Accept all and publish" }));
    await screen.findByText("Your post is live");
    const puts = calls.filter((c) => c.method === "PUT");
    expect(puts.map((c) => [c.url, c.body])).toEqual([
      ["/api/blog/posts/p1/blocks/b1", { decision: "accepted", ack: true }],
      ["/api/blog/posts/p1/blocks/b2", { decision: "accepted", ack: true }],
    ]);
    const publish = calls.find((c) => c.url.endsWith("/publish"))!;
    expect(publish.body).toEqual({ mode: "enriched" });
    expect(calls.indexOf(publish)).toBeGreaterThan(calls.indexOf(puts[1])); // only after the last one is saved
  });

  it("stops where it is when a choice cannot be saved, and publishes nothing", async () => {
    const calls = serve((c) => (c.method === "PUT" && c.url.endsWith("/b2") ? json({ error: { code: "ERROR" } }, 500) : null));
    mount(check());
    for (const n of [0, 1]) fireEvent.click(within(blockEl(n)).getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Accept all and publish" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not save that choice/i);
    expect(calls.some((c) => c.url.endsWith("/publish"))).toBe(false);
    expect(screen.queryByText("Your post is live")).toBeNull();
  });

  it("publishes the original on its own button", async () => {
    const calls = serve();
    mount(check());
    fireEvent.click(screen.getByRole("button", { name: "Publish my original" }));
    await screen.findByText("Your post is live");
    expect(calls.find((c) => c.url.endsWith("/publish"))!.body).toEqual({ mode: "original" });
  });
});

describe("deciding block by block", () => {
  it("Accept waits for that block's tick; once a block is decided the main button publishes what was accepted", async () => {
    serve();
    mount(check());
    const accept = within(blockEl(0)).getByRole("button", { name: "Accept" }) as HTMLButtonElement;
    expect(accept.disabled).toBe(true);
    fireEvent.click(within(blockEl(0)).getByRole("checkbox"));
    expect(accept.disabled).toBe(false);
    fireEvent.click(accept);
    await waitFor(() => expect(screen.getByRole("button", { name: "Publish with 1 block" })).toBeTruthy());
    expect(screen.queryByRole("button", { name: "Accept all and publish" })).toBeNull();
    fireEvent.click(within(blockEl(1)).getByRole("button", { name: "Reject" }));
    await waitFor(() => expect(within(blockEl(1)).getByRole("button", { name: "Rejected" })).toBeTruthy());
    expect((screen.getByRole("button", { name: "Publish with 1 block" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("with nothing accepted after deciding, there is nothing to publish but the original", async () => {
    serve();
    mount(check({ blocks: [block("b1")] }));
    fireEvent.click(within(blockEl(0)).getByRole("button", { name: "Reject" }));
    await waitFor(() => expect((screen.getByRole("button", { name: "Publish with 0 blocks" }) as HTMLButtonElement).disabled).toBe(true));
  });
});

describe("the sources of the selected block", () => {
  it("lists the first block's sources, and the drawer follows the block the author touches", () => {
    serve();
    mount(check());
    const drawer = () => screen.getByRole("complementary", { name: "Sources for the selected block" });
    expect(within(drawer()).getByText("About b1.example.org")).toBeTruthy();
    expect(within(drawer()).getByText("unverified")).toBeTruthy();
    fireEvent.click(blockEl(1));
    expect(within(drawer()).getByText("About b2.example.org")).toBeTruthy();
    expect(within(drawer()).queryByText("About b1.example.org")).toBeNull();
    expect(blockEl(1).className).toContain("selected");
    expect(blockEl(0).className).not.toContain("selected");
  });

  it("says so when a block names no sources, and a block with none needs no tick", () => {
    serve();
    mount(check({ blocks: [block("b1", { sources: [], needsAck: false })] }));
    expect(screen.getByText("This block names no sources.")).toBeTruthy();
    expect(within(blockEl(0)).queryByRole("checkbox")).toBeNull();
    expect((within(blockEl(0)).getByRole("button", { name: "Accept" }) as HTMLButtonElement).disabled).toBe(false);
    expect((screen.getByRole("button", { name: "Accept all and publish" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("each block that needs a tick says how many sources are unverified and what that means", () => {
    serve();
    mount(check({ blocks: [block("b1", { sources: [src("a.example.org"), src("b.example.org")] })] }));
    expect(within(blockEl(0)).getByText("2 unverified sources")).toBeTruthy();
    expect(within(blockEl(0)).getByText(/Antares has not checked these sources/)).toBeTruthy();
    expect(within(blockEl(0)).getByLabelText("I have checked these sources myself")).toBeTruthy();
  });
});

describe("nothing proposed", () => {
  it("says so, and Publish my original is the one button", () => {
    serve();
    mount(check({ blocks: [] }));
    expect(screen.getByText("The assistant proposed no blocks. Your writing is unchanged.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Accept all and publish" })).toBeNull();
    expect(screen.getByRole("button", { name: "Publish my original" }).className).toContain("btn");
  });

  it("shows what the server dropped", () => {
    serve();
    mount(check({ notices: ["One block was removed: it used something this site does not allow."] }));
    expect(screen.getByRole("status").textContent).toMatch(/One block was removed/);
  });
});

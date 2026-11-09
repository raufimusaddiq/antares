// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BlogApp } from "./BlogApp";
import { PublishFlow } from "./PublishFlow";
import type { DocNode, Post } from "./types";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json" } });
const doc: DocNode = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "Body" }] }] };
const post = (over: Partial<Post> = {}): Post => ({ id: "p1", title: "A title", summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc, check: null, updatedAt: "", ...over }) as Post;
const CAN = { writing: true, check: true, webSearch: true };
const CANNOT = { writing: true, check: false, webSearch: false };

function flow(over: { post?: Post; caps?: typeof CAN; onBack?: () => void } = {}) {
  const onBack = over.onBack ?? vi.fn();
  render(<PublishFlow post={over.post ?? post()} caps={over.caps ?? CAN} flush={async () => true} onPost={() => {}} onBackToEditing={onBack} onMyPosts={() => {}} />);
  return onBack;
}

describe("Publish is a dialog over the editor", () => {
  it("is a modal dialog named for what it does, with the design's two choices", () => {
    vi.stubGlobal("fetch", async () => json({}, 500));
    flow();
    const dlg = screen.getByRole("dialog", { name: "Publish this post" });
    expect(dlg.getAttribute("aria-modal")).toBe("true");
    expect(within(dlg).getByRole("heading", { name: "Check and enrich" })).toBeTruthy();
    expect(within(dlg).getByText(/checks claims against the web/)).toBeTruthy();
    expect(within(dlg).getByRole("heading", { name: "Publish my original" })).toBeTruthy();
    expect(within(dlg).getByText("Publish exactly what you wrote. No blocks are added.")).toBeTruthy();
  });

  it("moves focus to the first choice that can be taken, and Escape or Cancel goes back to editing", () => {
    vi.stubGlobal("fetch", async () => json({}, 500));
    const back = flow();
    expect(document.activeElement?.textContent).toBe("Check and enrich");
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(back).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(back).toHaveBeenCalledTimes(2);
  });

  it("starts on the other choice when checking cannot be taken, so focus is never left on a disabled button", () => {
    vi.stubGlobal("fetch", async () => json({}, 500));
    flow({ post: post({ title: "" }) });
    expect(screen.getByRole("alert").textContent).toMatch(/Give the post a title/);
    expect((screen.getByRole("button", { name: "Check and enrich" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Publish my original" }) as HTMLButtonElement).disabled).toBe(true);
    expect(document.activeElement?.textContent).toBe("Cancel"); // nothing to take: the way out
  });

  it("keeps focus in the dialog when the limits arrive and the check turns out to be too long", async () => {
    const LIMITS = { checkBytes: 50, docBytes: 524288, imagesPerPost: 30, imageBytes: 5242880, imageBytesTotal: 209715200, blocksPerCheck: 10, sourcesPerBlock: 10, checksPer24h: 30 };
    vi.stubGlobal("fetch", async () => json(LIMITS));
    flow({ post: post({ doc: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "word ".repeat(60) }] }] } }) });
    expect(document.activeElement?.textContent).toBe("Check and enrich"); // before the limits are known it can be taken
    await waitFor(() => expect((screen.getByRole("button", { name: "Check and enrich" }) as HTMLButtonElement).disabled).toBe(true));
    // The effect that puts focus back runs just after the commit that disabled the button: wait for it, not for the commit.
    await waitFor(() => expect(document.activeElement?.textContent).toBe("Publish my original"));
    expect(screen.getByRole("dialog").contains(document.activeElement)).toBe(true);
  });

  it("says plainly when checking is not available, and makes the original the one choice", () => {
    vi.stubGlobal("fetch", async () => json({}, 500));
    flow({ caps: CANNOT });
    const dlg = screen.getByRole("dialog");
    expect(within(dlg).getByText("Checking is not available right now")).toBeTruthy();
    expect(within(dlg).getByText("The assistant or its web search is not set up. You can still publish your post exactly as written.")).toBeTruthy();
    expect(within(dlg).queryByRole("button", { name: "Check and enrich" })).toBeNull();
    expect(document.activeElement?.textContent).toBe("Publish my original");
  });

  it("goes to the check when asked, and the check covers the screen instead of sitting in the dialog", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", async (u: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? "GET"} ${u}`);
      return u.endsWith("/check") ? json({ id: "c1", state: "running", blocks: [], notes: [], notices: [], startedAt: "", finishedAt: "" }, 202) : json({}, 500);
    });
    flow();
    fireEvent.click(screen.getByRole("button", { name: "Check and enrich" }));
    await waitFor(() => expect(calls).toContain("POST /api/blog/posts/p1/check"));
    const heading = await screen.findByRole("heading", { name: "Checking your writing" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.querySelector(".blog-takeover")).not.toBeNull();
    expect(document.activeElement).toBe(heading); // the new step is announced and the keyboard starts at its top
  });
});

describe("Publish over the editor", () => {
  const server = (p: Post) =>
    vi.stubGlobal("fetch", async (u: string) => (u === "/api/blog/limits" ? json({}, 500) : json(p)));
  const app = (kind: "edit" | "publish") => render(<BlogApp caps={CAN} email="a@example.com" route={{ kind, id: "p1" }} onNavigate={() => {}} onChats={() => {}} />);

  it("keeps the editor behind the dialog and out of reach while it is open", async () => {
    server(post());
    app("publish");
    await screen.findByRole("dialog", { name: "Publish this post" });
    await waitFor(() => expect(document.querySelector(".blog-behind .blog-editor")).not.toBeNull()); // the editor is there to be seen behind the scrim
    const behind = document.querySelector(".blog-behind") as HTMLElement;
    expect(behind.hasAttribute("inert")).toBe(true);
    expect(behind.getAttribute("aria-hidden")).toBe("true");
  });

  it("puts focus back on the editor's Publish button when Publish closes (the button that had it before is gone)", async () => {
    server(post());
    const { rerender } = app("publish");
    await screen.findByRole("dialog", { name: "Publish this post" });
    await waitFor(() => expect(document.querySelector(".blog-behind .blog-editor")).not.toBeNull());
    rerender(<BlogApp caps={CAN} email="a@example.com" route={{ kind: "edit", id: "p1" }} onNavigate={() => {}} onChats={() => {}} />);
    await waitFor(() => expect(document.activeElement?.hasAttribute("data-publish")).toBe(true));
    expect(document.activeElement?.textContent).toBe("Publish");
  });

  it("does not take focus when the editor is simply opened", async () => {
    server(post());
    app("edit");
    await screen.findByLabelText("Title");
    expect(document.activeElement?.hasAttribute("data-publish")).toBe(false);
  });

  it("is not inert when Publish is closed", async () => {
    server(post());
    app("edit");
    await screen.findByLabelText("Title");
    const behind = document.querySelector(".blog-behind") as HTMLElement;
    expect(behind.hasAttribute("inert")).toBe(false);
    expect(behind.hasAttribute("aria-hidden")).toBe(false);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

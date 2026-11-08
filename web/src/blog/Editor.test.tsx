// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Editor from "./Editor";
import { EMPTY_DOC, type Post } from "./types";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

const post: Post = { id: "p1abcdef", title: "Server title", summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: 3, publishedVersion: 0, doc: EMPTY_DOC, check: null, updatedAt: "" };
const props = { post, email: "a@example.com", onPost: () => {}, onSettings: () => {}, onPublish: () => {}, onBack: () => {} };

describe("editor", () => {
  it("restores what this browser kept after a reload, and sends it to the server with the version it was opened at", async () => {
    // What a reload left behind: text typed after the last confirmed save.
    localStorage.setItem("antares:drafts-owner", encodeURIComponent("a@example.com"));
    localStorage.setItem(
      `antares:draft:v1:${encodeURIComponent("a@example.com")}:chat:post:p1abcdef`,
      JSON.stringify({ base: 3, title: "Typed offline", doc: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "kept text" }] }] } }),
    );
    const bodies: unknown[] = [];
    vi.stubGlobal("fetch", async (_u: string, init: RequestInit) => {
      bodies.push(JSON.parse(String(init.body)));
      return new Response("{}", { status: 500 }); // the server is not reachable for this test: the copy must survive
    });
    render(<Editor {...props} />);
    expect((screen.getByLabelText("Title") as HTMLInputElement).value).toBe("Typed offline");
    await waitFor(() => expect(document.body.textContent).toContain("kept text"));
    await waitFor(() => expect(bodies.length).toBeGreaterThan(0), { timeout: 3000 });
    expect(bodies[0]).toMatchObject({ baseVersion: 3, title: "Typed offline" });
    expect(localStorage.length).toBeGreaterThan(0); // still held: the server has not confirmed it
  });

  it("never sends a kept copy over a newer server version: it waits for the author's choice", async () => {
    localStorage.setItem(
      `antares:draft:v1:${encodeURIComponent("a@example.com")}:chat:post:p1abcdef`,
      JSON.stringify({ base: 2, title: "Typed on v2" }), // the server is at v3 now
    );
    const bodies: unknown[] = [];
    vi.stubGlobal("fetch", async (_u: string, init: RequestInit) => {
      bodies.push(init.body);
      return new Response("{}", { status: 500 });
    });
    render(<Editor {...props} />);
    await new Promise((r) => setTimeout(r, 1200));
    expect(bodies).toHaveLength(0);
    expect((screen.getByLabelText("Title") as HTMLInputElement).value).toBe("Typed on v2"); // kept, shown
    expect(localStorage.length).toBeGreaterThan(0);
  });

  it("opens the server's text when this browser holds nothing newer", () => {
    render(<Editor {...props} />);
    expect((screen.getByLabelText("Title") as HTMLInputElement).value).toBe("Server title");
    expect(screen.getByRole("status").textContent).toBe("Saved");
  });
});

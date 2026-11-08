// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BlogApp } from "./BlogApp";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// (the probe itself lives in App; BlogApp is handed its answer)
const reply = (body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));

describe("blog author area", () => {
  it("shows nothing of the editor or My posts when the server says writing is unavailable", async () => {
    const f = reply({});
    vi.stubGlobal("fetch", f);
    render(<BlogApp caps={{ writing: false, check: false, webSearch: false }} email="a@example.com" route={{ kind: "list" }} onNavigate={() => {}} onChats={() => {}} />);
    await waitFor(() => expect(screen.getByText(/not available right now/i)).toBeTruthy());
    expect(screen.queryByText(/new post/i)).toBeNull();
    expect(f).not.toHaveBeenCalled(); // My posts was never asked for
  });
});

describe("publishing a new post", () => {
  it("sees the title the author typed and the server saved, not the empty one the post opened with", async () => {
    const empty = { id: "p1abcdef", title: "", summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc: { type: "doc", content: [] }, check: null, updatedAt: "" };
    const saved = { ...empty, title: "Hello", latestVersion: 2 };
    vi.stubGlobal("fetch", async (_u: string, init?: RequestInit) => new Response(JSON.stringify(init?.method === "PUT" ? saved : empty), { status: 200, headers: { "Content-Type": "application/json" } }));
    const nav = vi.fn();
    const caps = { writing: true, check: true, webSearch: false };
    const { rerender } = render(<BlogApp caps={caps} email="a@example.com" route={{ kind: "edit", id: "p1abcdef" }} onNavigate={nav} onChats={() => {}} />);
    const title = (await screen.findByLabelText("Title")) as HTMLInputElement;
    fireEvent.change(title, { target: { value: "Hello" } });
    fireEvent.click(screen.getByRole("button", { name: /publish/i }));
    await waitFor(() => expect(nav).toHaveBeenCalledWith("/blog/p1abcdef/publish"));
    rerender(<BlogApp caps={caps} email="a@example.com" route={{ kind: "publish", id: "p1abcdef" }} onNavigate={nav} onChats={() => {}} />);
    const go = await screen.findAllByRole("button", { name: /check|publish/i });
    expect(go.some((b) => !(b as HTMLButtonElement).disabled)).toBe(true);
  });
});

describe("back to editing after a publish attempt", () => {
  it("reopens on the text the server confirmed, not the text the post was first fetched with", async () => {
    const first = { id: "p1abcdef", title: "T", summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "OLD" }] }] }, check: null, updatedAt: "" };
    const saved = { ...first, latestVersion: 2, doc: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "NEW" }] }] } };
    vi.stubGlobal("fetch", async (_u: string, init?: RequestInit) => new Response(JSON.stringify(init?.method === "PUT" ? saved : first), { status: 200, headers: { "Content-Type": "application/json" } }));
    const caps = { writing: true, check: true, webSearch: false };
    const nav = vi.fn();
    const { rerender } = render(<BlogApp caps={caps} email="a@example.com" route={{ kind: "edit", id: "p1abcdef" }} onNavigate={nav} onChats={() => {}} />);
    fireEvent.change(await screen.findByLabelText("Title"), { target: { value: "T2" } });
    fireEvent.click(screen.getByRole("button", { name: /publish/i }));
    await waitFor(() => expect(nav).toHaveBeenCalledWith("/blog/p1abcdef/publish"));
    rerender(<BlogApp caps={caps} email="a@example.com" route={{ kind: "publish", id: "p1abcdef" }} onNavigate={nav} onChats={() => {}} />);
    await screen.findAllByRole("button", { name: /check|publish/i });
    rerender(<BlogApp caps={caps} email="a@example.com" route={{ kind: "edit", id: "p1abcdef" }} onNavigate={nav} onChats={() => {}} />);
    await waitFor(() => expect(document.body.textContent).toContain("NEW"));
    expect(document.body.textContent).not.toContain("OLD");
  });
});

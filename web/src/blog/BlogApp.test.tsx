// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BlogApp } from "./BlogApp";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const reply = (body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));

describe("blog author area", () => {
  it("shows nothing of the editor or My posts when the server says writing is unavailable", async () => {
    const f = reply({ writing: false, check: false, webSearch: false });
    vi.stubGlobal("fetch", f);
    render(<BlogApp email="a@example.com" route={{ kind: "list" }} onNavigate={() => {}} onChats={() => {}} />);
    await waitFor(() => expect(screen.getByText(/not available right now/i)).toBeTruthy());
    expect(screen.queryByText(/new post/i)).toBeNull();
    expect(f).toHaveBeenCalledTimes(1); // the capability probe only; My posts was never asked for
  });
});

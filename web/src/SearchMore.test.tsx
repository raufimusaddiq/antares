// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SearchPanel } from "./Search";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const body = (cursor: string) =>
  JSON.stringify({
    results: [{ chatId: "a".repeat(32), chatTitle: "T", kind: "message", turn: 1, role: "user", createdAt: "", snippet: [{ text: "x", hit: true }] }],
    nextCursor: cursor,
    index: { complete: true, chats: 1, pending: 0, failed: 0, partial: 0 },
  });

describe("More results belongs to the query that produced it", () => {
  it("repeats the searched text and filters after the box has been edited", async () => {
    const f = vi.fn(async (_u: string) => new Response(body(f.mock.calls.length === 1 ? "cur1" : ""), { status: 200 }));
    vi.stubGlobal("fetch", f);
    render(<SearchPanel current={null} onOpen={() => {}} />);
    const box = screen.getByRole("searchbox");
    fireEvent.change(box, { target: { value: "alpha" } });
    fireEvent.submit(box.closest("form")!);
    await screen.findByText("More results");
    fireEvent.change(box, { target: { value: "beta" } }); // edited, not submitted
    fireEvent.click(screen.getByText("More results"));
    await waitFor(() => expect(f).toHaveBeenCalledTimes(2));
    const second = String(f.mock.calls[1][0]);
    expect(second).toContain("q=alpha");
    expect(second).not.toContain("beta");
    expect(second).toContain("cursor=cur1");
  });
});

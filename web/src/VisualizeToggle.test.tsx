// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChatSummary } from "./chatApi";
import { OpenChatVisualize, VisualizeToggle } from "./VisualizeToggle";


const chat: ChatSummary = { id: "c1", title: "T", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false };

// #153: the toggle saves per chat through PATCH and shows what the server stored.
describe("VisualizeToggle", () => {
  it("sends the change for this chat and reports the server's answer", async () => {
    const calls: { path: string; body: string }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init?: RequestInit) => {
        calls.push({ path, body: String(init?.body) });
        return new Response(JSON.stringify({ ...chat, visualize: true }), { status: 200 });
      }),
    );
    const changed = vi.fn();
    render(<VisualizeToggle chat={chat} onChanged={changed} />);
    fireEvent.click(screen.getByRole("checkbox"));
    await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ id: "c1", visualize: true })));
    expect(calls).toEqual([{ path: "/api/chats/c1", body: '{"visualize":true}' }]);
  });

  it("explains a refusal and leaves the stored state", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: "x" } }), { status: 404 })));
    const changed = vi.fn();
    render(<VisualizeToggle chat={chat} onChanged={changed} />);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(changed).not.toHaveBeenCalled();
    expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
  });
});

// #198 review: a chat opened from a search result outside the loaded list still shows its toggle (Chat passes the chat
// search read by id; SearchMore.test.tsx covers opening such a result).
it("shows the toggle for the chat it is given, and nothing without one", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ ...chat, id: "c9", visualize: false }), { status: 200 })));
  const { rerender } = render(<OpenChatVisualize chat={undefined} onChanged={() => {}} />);
  expect(screen.queryByRole("checkbox")).toBeNull();
  const changed = vi.fn();
  rerender(<OpenChatVisualize chat={{ ...chat, id: "c9", visualize: true }} onChanged={changed} />);
  expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(true);
  fireEvent.click(screen.getByRole("checkbox"));
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ id: "c9", visualize: false })));
});

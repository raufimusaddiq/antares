// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ChatSummary } from "./chatApi";
import { OpenChatVisualize, VisualizeToggle } from "./VisualizeToggle";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

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

// #198 review: a chat opened from a search result outside the loaded list still shows its toggle, read by id.
it("shows the toggle for an open chat that is not in the loaded list", async () => {
  const asked: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      asked.push(`${init?.method ?? "GET"} ${path}`);
      return new Response(JSON.stringify({ ...chat, id: "c9", visualize: init?.method === "PATCH" ? false : true }), { status: 200 });
    }),
  );
  render(<OpenChatVisualize chatId="c9" listed={undefined} onChanged={() => {}} />);
  const box = (await screen.findByRole("checkbox")) as HTMLInputElement;
  expect(box.checked).toBe(true);
  expect(asked).toEqual(["GET /api/chats/c9"]);
  fireEvent.click(box);
  await waitFor(() => expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false));
});

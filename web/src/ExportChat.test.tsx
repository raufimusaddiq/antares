// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ApiResult, HistoryPage } from "./chatApi";

const getMessages = vi.fn<(chatId: string, cursor?: string) => Promise<ApiResult<HistoryPage>>>();
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), getMessages: (c: string, cur?: string) => getMessages(c, cur) }));
import { ExportChat } from "./ExportChat";

const page = (text: string, nextCursor = ""): ApiResult<HistoryPage> => ({ ok: true, value: { turns: [{ seq: 0, role: "assistant", text }], nextCursor, total: 1 } });
let saved: string[]; // text of each Blob handed to createObjectURL

beforeEach(() => {
  getMessages.mockReset();
  saved = [];
  vi.stubGlobal("Blob", class { constructor(public parts: string[]) {} });
  URL.createObjectURL = (b: Blob | MediaSource) => (saved.push((b as unknown as { parts: string[] }).parts.join("")), "blob:x");
  URL.revokeObjectURL = () => {};
  HTMLAnchorElement.prototype.click = () => {};
  window.print = vi.fn();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("ExportChat", () => {
  it("saves nothing, and fetches no further page, once the user has moved to another chat", async () => {
    let release!: (r: ApiResult<HistoryPage>) => void;
    getMessages.mockImplementationOnce(() => new Promise((r) => (release = r)));
    const { rerender } = render(<ExportChat chatId="a" title="A" />);
    fireEvent.click(screen.getByRole("button", { name: "Export Markdown" }));
    rerender(<ExportChat chatId="b" title="B" />); // the component survives a chat switch
    await act(async () => release(page("secret of chat a", "next")));
    expect(saved).toHaveLength(0);
    expect(getMessages).toHaveBeenCalledTimes(1); // the cursor for a second page was not followed
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("downloads the Markdown of the open chat", async () => {
    getMessages.mockResolvedValueOnce(page("hello"));
    render(<ExportChat chatId="a" title="A" />);
    fireEvent.click(screen.getByRole("button", { name: "Export Markdown" }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(saved[0]).toContain("## Assistant\n\nhello");
  });

  it("prints a copy of the whole chat, then removes it after printing", async () => {
    getMessages.mockResolvedValueOnce(page("printed answer"));
    render(<ExportChat chatId="a" title="A" />);
    fireEvent.click(screen.getByRole("button", { name: "Export PDF" }));
    await waitFor(() => expect(window.print).toHaveBeenCalledTimes(1));
    expect(document.body.querySelector(".print-root")?.textContent).toContain("printed answer");
    act(() => void window.dispatchEvent(new Event("afterprint")));
    expect(document.body.querySelector(".print-root")).toBeNull();
  });

  it("shows a failure, saves nothing and re-enables the buttons", async () => {
    getMessages.mockResolvedValueOnce({ ok: false, code: "NOT_FOUND", status: 404 });
    render(<ExportChat chatId="a" title="A" />);
    fireEvent.click(screen.getByRole("button", { name: "Export Markdown" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Export failed");
    expect(saved).toHaveLength(0);
    expect((screen.getByRole("button", { name: "Export PDF" }) as HTMLButtonElement).disabled).toBe(false);
  });
});

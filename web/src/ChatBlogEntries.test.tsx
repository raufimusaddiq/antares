// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

const api = vi.hoisted(() => ({ listChats: vi.fn(), getMessages: vi.fn(), getRun: vi.fn() }));
vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));

import { Chat } from "./Chat";

class FakeSource {
  static CLOSED = 2;
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  addEventListener() {}
  close() {}
}

const mount = async (props: { onBlog?: () => void; onReadBlog?: () => void }) => {
  api.listChats.mockResolvedValue({ ok: true, value: { chats: [], nextCursor: "" } });
  render(<Chat email="a@example.test" onSignOut={() => {}} {...props} />);
  await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
};
const rail = () => document.querySelector("nav.side") as HTMLElement;

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeSource);
  api.getMessages.mockResolvedValue({ ok: true, value: { turns: [], nextCursor: "", total: 0 } });
  api.getRun.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Object.values(api).forEach((m) => m.mockReset());
});

// Spec 003 FR-003: every approved user can reach the public blog from the chat, in the menu and in the rail, apart from their own workspace.
describe("Read the blog in the chat", () => {
  it("a writer has Blog posts and Read the blog as two different controls, in the rail and in the menu", async () => {
    const own = vi.fn();
    const read = vi.fn();
    await mount({ onBlog: own, onReadBlog: read });
    within(rail()).getByRole("button", { name: "Blog posts" });
    fireEvent.click(within(rail()).getByRole("button", { name: "Read the blog" }));
    expect(read).toHaveBeenCalledTimes(1);
    expect(own).not.toHaveBeenCalled();
    fireEvent.click(screen.getAllByRole("button", { name: "Menu" })[0]);
    const menu = document.getElementById("bar-actions") as HTMLElement;
    within(menu).getByRole("button", { name: "Blog posts" });
    fireEvent.click(within(menu).getByRole("button", { name: "Read the blog" }));
    expect(read).toHaveBeenCalledTimes(2);
  });

  it("the rail button beside Blog posts adds no row", async () => {
    await mount({ onBlog: () => {}, onReadBlog: () => {} });
    const row = within(rail()).getByRole("button", { name: "Blog posts" }).parentElement as HTMLElement;
    expect(within(row).getAllByRole("button")).toHaveLength(2); // the pill and the icon button share one row
  });

  it("a user without writing still gets Read the blog, as the one rail button, and no Blog posts", async () => {
    await mount({ onReadBlog: () => {} });
    expect(within(rail()).getAllByRole("button", { name: "Read the blog" })).toHaveLength(1);
    expect(within(rail()).getByRole("button", { name: "Read the blog" }).textContent).toContain("Read the blog");
    expect(screen.queryByRole("button", { name: "Blog posts" })).toBeNull();
  });

  it("without the entry nothing is shown", async () => {
    await mount({});
    expect(screen.queryByRole("button", { name: "Read the blog" })).toBeNull();
  });
});

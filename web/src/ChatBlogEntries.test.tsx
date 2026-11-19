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
const menu = () => {
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  return document.getElementById("bar-actions") as HTMLElement;
};

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

// Spec 003 FR-003: every approved user reaches the public blog from the rail and the menu. T-7 keeps "Blog posts" in
// one place (the menu) and leaves the rail's "Read the blog" as the only rail entry.
describe("Read the blog in the chat", () => {
  it("a writer has Read the blog in the rail and in the Menu, apart from Blog posts", async () => {
    const own = vi.fn();
    const read = vi.fn();
    await mount({ onBlog: own, onReadBlog: read });
    const pill = within(rail()).getByRole("button", { name: "Read the blog" });
    expect(within(rail()).queryByRole("button", { name: "Blog posts" })).toBeNull(); // once, in the Menu
    fireEvent.click(pill);
    expect(read).toHaveBeenCalledTimes(1);
    expect(own).not.toHaveBeenCalled();
    const panel = menu();
    expect(within(panel).getByRole("button", { name: "Blog posts" })).toBeTruthy();
    fireEvent.click(within(panel).getByRole("button", { name: "Read the blog" }));
    expect(read).toHaveBeenCalledTimes(2);
  });

  it("a user without writing still gets Read the blog in the rail and the Menu, and no Blog posts", async () => {
    await mount({ onReadBlog: () => {} });
    expect(within(rail()).getAllByRole("button", { name: "Read the blog" })).toHaveLength(1);
    expect(within(menu()).getByRole("button", { name: "Read the blog" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Blog posts" })).toBeNull();
  });

  it("without the entry nothing is shown", async () => {
    await mount({});
    expect(screen.queryByRole("button", { name: "Read the blog" })).toBeNull();
  });
});

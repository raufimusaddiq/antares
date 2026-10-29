// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { VirtualList } from "./VirtualList";

let height = 1200;
let viewport = 400;
let top = 0;
let resized: () => void;
const key = (x: string) => x;
const props = { items: ["a", "b", "c"], itemKey: key, render: key, label: "Conversation", hasMore: false, loadingMore: false, topNote: "", onLoadMore: () => {} };

beforeEach(() => {
  height = 1200;
  viewport = 400;
  top = 0;
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(() => height);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(() => viewport);
  vi.spyOn(Element.prototype, "scrollTop", "get").mockImplementation(() => top);
  vi.spyOn(Element.prototype, "scrollTop", "set").mockImplementation((y) => { top = Math.max(0, Math.min(y, height - viewport)); });
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: () => void) { resized = callback; }
    observe() {}
    disconnect() {}
  });
});
afterEach(() => {vi.restoreAllMocks();});

it("follows streaming at the bottom, holds the reader's position above it, and resumes on jump", () => {
  const { rerender } = render(<VirtualList {...props} footer="First delta" />);
  const feed = screen.getByRole("feed");
  expect(feed.scrollTop).toBe(800);
  height = 1400;
  rerender(<VirtualList {...props} footer="More streaming text" />);
  expect(feed.scrollTop).toBe(1000);
  feed.scrollTop = 300;
  // A render can arrive before the browser dispatches its next scroll event.
  height = 1600;
  rerender(<VirtualList {...props} footer="Another delta" />);
  expect(feed.scrollTop).toBe(300);
  expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();
  fireEvent.scroll(feed);
  act(() => { height = 1800; resized(); });
  expect(feed.scrollTop).toBe(300);
  fireEvent.click(screen.getByRole("button", { name: "Jump to latest" }));
  expect(feed.scrollTop).toBe(1400);
  height = 2000;
  rerender(<VirtualList {...props} footer="Final delta" />);
  expect(feed.scrollTop).toBe(1600);
  expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
});

it("keeps following when the viewport shrinks for the phone keyboard or a taller composer", () => {
  render(<VirtualList {...props} footer="Streaming" />);
  expect(top).toBe(800);
  act(() => { viewport = 250; resized(); });
  expect(top).toBe(950);
  expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
});

it("holds the reader's position when the viewport shrinks while scrolled up", () => {
  render(<VirtualList {...props} footer="Streaming" />);
  const feed = screen.getByRole("feed");
  feed.scrollTop = 300;
  fireEvent.scroll(feed);
  act(() => { viewport = 250; resized(); });
  expect(feed.scrollTop).toBe(300);
  expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();
});

// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { VirtualList } from "./VirtualList";

// #170: a fling renders on every frame. Re-reading the height of every mounted item on each of those renders was the
// largest measured cost (one forced layout per frame, growing with the history), so a render that mounts nothing new
// must not measure anything; the ResizeObserver re-reads items that change on their own.
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("measuring mounted items (#170)", () => {
  it("measures each item once, not again on a scroll that mounts nothing new", () => {
    const rect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
      () => ({ height: 40, width: 600, top: 0, left: 0, right: 600, bottom: 40, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect,
    );
    const items = Array.from({ length: 20 }, (_, i) => `m${i}`);
    const { container } = render(
      <VirtualList items={items} itemKey={(x) => x} render={(x) => <p>{x}</p>} label="Conversation" hasMore={false} loadingMore={false} topNote="" onLoadMore={() => {}} />,
    );
    const firstPass = rect.mock.calls.length;
    expect(firstPass).toBeGreaterThanOrEqual(items.length); // every item measured once on mount
    const log = container.querySelector(".log")!;
    for (let i = 0; i < 10; i++) {
      act(() => {
        log.scrollTop = 40 * i;
        fireEvent.scroll(log);
      });
    }
    expect(rect.mock.calls.length).toBe(firstPass);
  });
});

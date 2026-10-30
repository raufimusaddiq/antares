// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render } from "@testing-library/react";

const virtual = vi.hoisted(() => ({ layout: undefined as unknown as ReturnType<typeof vi.fn> }));
vi.mock("./virtual", async (orig) => {
  const m = await orig<typeof import("./virtual")>();
  virtual.layout = vi.fn(m.layout);
  return { ...m, layout: virtual.layout };
});

import { VirtualList } from "./VirtualList";

// #170: a fling renders on every frame. Re-reading the height of every mounted item and rebuilding the offset table of
// the whole loaded history on each of those renders were the measured costs, so a render that mounts nothing new must do
// neither; the ResizeObserver re-reads items that change on their own.
afterEach(() => {
  vi.restoreAllMocks();
});

const items = Array.from({ length: 20 }, (_, i) => `m${i}`);
const list = () => (
  <VirtualList items={items} itemKey={(x) => x} render={(x) => <p>{x}</p>} label="Conversation" hasMore={false} loadingMore={false} topNote="" onLoadMore={() => {}} />
);
const rect = () =>
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    () => ({ height: 40, width: 600, top: 0, left: 0, right: 600, bottom: 40, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect,
  );
const scroll = (log: Element, n: number) => {
  for (let i = 0; i < n; i++) {
    act(() => {
      log.scrollTop = 40 * i;
      fireEvent.scroll(log);
    });
  }
};

describe("a scroll that mounts nothing new (#170)", () => {
  it("measures no item again", () => {
    const r = rect();
    const { container } = render(list());
    const first = r.mock.calls.length;
    expect(first).toBeGreaterThanOrEqual(items.length); // every item measured once on mount
    scroll(container.querySelector(".log")!, 10);
    expect(r.mock.calls.length).toBe(first);
  });

  it("does not rebuild the offset table", () => {
    rect();
    const { container } = render(list());
    const built = virtual.layout.mock.calls.length;
    scroll(container.querySelector(".log")!, 10);
    expect(virtual.layout.mock.calls.length).toBe(built);
  });

  it("renders no item again (a chat of rich cards stuttered)", () => {
    rect();
    const draw = vi.fn((x: string) => <p>{x}</p>);
    const { container } = render(
      <VirtualList items={items} itemKey={(x) => x} render={draw} label="Conversation" hasMore={false} loadingMore={false} topNote="" onLoadMore={() => {}} />,
    );
    draw.mockClear();
    scroll(container.querySelector(".log")!, 10);
    expect(draw).not.toHaveBeenCalled();
  });
});

describe("a width change (#170 review)", () => {
  it("forgets the heights measured at the old width, also of items no longer mounted", () => {
    let observed: (() => void) | null = null;
    vi.stubGlobal("ResizeObserver", class {
      constructor(cb: () => void) {
        observed = cb;
      }
      observe() {}
      disconnect() {}
    });
    let width = 600;
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(() => width);
    rect();
    const long = Array.from({ length: 300 }, (_, i) => `m${i}`); // windowed: opens at the newest end
    const { container } = render(
      <VirtualList items={long} itemKey={(x) => x} render={(x) => <p>{x}</p>} label="Conversation" hasMore={false} loadingMore={false} topNote="" onLoadMore={() => {}} />,
    );
    act(() => observed!()); // the first observation records the width
    const log = container.querySelector(".log")!;
    for (const y of [1e6, 0]) { // to the newest item (measured), then back to the start (it unmounts)
      act(() => {
        log.scrollTop = y;
        fireEvent.scroll(log);
      });
    }
    const heightsSeen = () => virtual.layout.mock.calls.at(-1)![1] as ReadonlyMap<string, number>;
    expect(container.querySelector('[data-vkey="m299"]')).toBeNull(); // the newest item is no longer mounted
    expect(heightsSeen().has("m299")).toBe(true); // ...but its height is still cached
    width = 320;
    act(() => observed!());
    expect(heightsSeen().has("m299")).toBe(false); // measured at 600 px: forgotten, re-measured when it mounts again
  });
});

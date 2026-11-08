// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Autosaver, loadLocalCopy, type SaveState } from "./autosave";
import type { BlogResult } from "./blogApi";
import { EMPTY_DOC, type Post } from "./types";

const post = (v: number, title = "t"): Post => ({ id: "p1", title, summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: v, publishedVersion: 0, doc: EMPTY_DOC, check: null, updatedAt: "" });
const email = "a@example.com";

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

function make(save: (id: string, base: number, c: unknown) => Promise<BlogResult<Post>>) {
  const states: SaveState[] = [];
  const s = new Autosaver({ id: "p1", email, version: 1, onState: (st) => states.push(st), save: save as never, delayMs: 10, retryMs: 50 });
  return { s, states };
}

describe("blog autosave", () => {
  it("keeps the text on this device until the server confirms it, then lets it go", async () => {
    const { s, states } = make(async () => ({ ok: true, value: post(2) }));
    s.change({ title: "Hello" });
    expect(loadLocalCopy(email, "p1")).toEqual({ title: "Hello" });
    await vi.advanceTimersByTimeAsync(20);
    expect(states.at(-1)).toBe("saved");
    expect(loadLocalCopy(email, "p1")).toBeNull();
  });

  it("sends the version it was opened at, and never forces a stale save over a newer one", async () => {
    const bases: number[] = [];
    const { s, states } = make(async (_id, base) => {
      bases.push(base);
      return { ok: false, status: 409, code: "ERROR", latest: post(5, "newer") };
    });
    s.change({ title: "mine" });
    await vi.advanceTimersByTimeAsync(20);
    expect(states.at(-1)).toBe("conflict");
    expect(bases).toEqual([1]);
    expect(loadLocalCopy(email, "p1")).toEqual({ title: "mine" }); // nothing lost
    s.change({ title: "mine 2" }); // still nothing is sent while the author has not chosen
    await vi.advanceTimersByTimeAsync(200);
    expect(bases).toEqual([1]);
    s.keepMine();
    await vi.advanceTimersByTimeAsync(20);
    expect(bases).toEqual([1, 5]); // the retry is based on the version the author has now seen
  });

  it("taking the saved version drops the local copy", async () => {
    const { s } = make(async () => ({ ok: false, status: 409, code: "ERROR", latest: post(5, "newer") }));
    s.change({ title: "mine" });
    await vi.advanceTimersByTimeAsync(20);
    expect(s.takeTheirs()?.title).toBe("newer");
    expect(loadLocalCopy(email, "p1")).toBeNull();
  });

  it("a lost connection is shown as offline, keeps the text and retries", async () => {
    let up = false;
    const { s, states } = make(async () => (up ? { ok: true, value: post(2) } : { ok: false, status: 0, code: "NETWORK" }));
    s.change({ title: "x" });
    await vi.advanceTimersByTimeAsync(20);
    expect(states.at(-1)).toBe("offline");
    expect(loadLocalCopy(email, "p1")).toEqual({ title: "x" });
    up = true;
    await vi.advanceTimersByTimeAsync(100);
    expect(states.at(-1)).toBe("saved");
  });
});

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

  it("flush says whether the server has everything: a failed save is not a saved one", async () => {
    const ok = make(async () => ({ ok: true, value: post(2) }));
    ok.s.change({ title: "a" });
    expect(await ok.s.flush()).toBe(true);

    const down = make(async () => ({ ok: false, status: 0, code: "NETWORK" }));
    down.s.change({ title: "b" });
    const f = down.s.flush();
    await vi.advanceTimersByTimeAsync(1);
    expect(await f).toBe(false);
    expect(down.states.at(-1)).toBe("offline");
    expect(loadLocalCopy(email, "p1")).toEqual({ title: "b" });
    down.s.dispose();
  });

  it("a 409 that names no newer version waits, kept, instead of resending the same change", async () => {
    const save = vi.fn(async () => ({ ok: false as const, status: 409, code: "ERROR" }));
    const { s, states } = make(save);
    s.change({ title: "x" });
    await vi.advanceTimersByTimeAsync(500);
    expect(save).toHaveBeenCalledTimes(1);
    expect(states.at(-1)).toBe("error");
    expect(loadLocalCopy(email, "p1")).toEqual({ title: "x" });
  });

  it("text typed while a save is in flight is recorded against the version that save produced", async () => {
    let release: (r: BlogResult<Post>) => void = () => {};
    const { s } = make(() => new Promise((res) => (release = res)));
    s.change({ title: "a" });
    await vi.advanceTimersByTimeAsync(20); // in flight
    s.change({ title: "ab" });
    release({ ok: true, value: post(4) });
    await vi.advanceTimersByTimeAsync(1);
    expect(JSON.parse(localStorage.getItem(`antares:draft:v1:${encodeURIComponent(email)}:chat:post:p1`) ?? "{}").base).toBe(4);
    s.dispose();
  });

  it("flush waits for text typed during a save and reports saved only when all of it is", async () => {
    let n = 0;
    const { s } = make(async () => {
      n++;
      if (n === 1) s.change({ title: "ab" }); // typed while the first save is in flight
      return { ok: true, value: post(1 + n) };
    });
    s.change({ title: "a" });
    expect(await s.flush()).toBe(true);
    expect(n).toBe(2);
    s.dispose();
  });
});

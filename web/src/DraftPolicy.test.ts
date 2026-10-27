// @vitest-environment jsdom
// #172: purge-on-stale-session. Every path that ends or changes a session is followed by a read of localStorage.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { loadSession, signOut } from "./api";
import { listChats } from "./chatApi";
import { DRAFT_TTL_MS, loadBaseRevision, loadDraft, purgeStaleDrafts, saveBaseRevision, saveDraft } from "./drafts";

const A = "a@x.test";
const B = "b@x.test";
const drafts = () => Object.keys(localStorage).filter((k) => k.startsWith("antares:draft:"));
const seed = (now = Date.now()) => {
  saveDraft(A, "c1", "A's unsent text", now);
  saveBaseRevision(A, "c1", 3);
  saveDraft(A, null, "A's new chat", now);
};

beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal("navigator", {}); // no service worker in these tests
});
afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("local drafts follow the session (#172)", () => {
  for (const [name, response, call] of [
    ["logout", new Response(null, { status: 204 }), signOut],
    ["expired session at start (401)", new Response(null, { status: 401 }), () => loadSession(false)],
    ["suspension at start", Response.json({ state: "SUSPENDED", email: A }), () => loadSession(false)],
    ["401 during use", new Response(null, { status: 401 }), listChats],
    ["suspension during use", Response.json({ error: { code: "ACCOUNT_SUSPENDED" } }, { status: 403 }), listChats],
  ] as const) {
    it(`${name} leaves no draft on the device`, async () => {
      seed();
      vi.stubGlobal("fetch", vi.fn(async () => response));
      await call();
      expect(drafts()).toEqual([]);
    });
  }

  it("typing after a 401 does not recreate the purged draft, until a session loads again", async () => {
    seed();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 401 })));
    await listChats();
    saveDraft(A, "c1", "typed after the session ended");
    expect(drafts()).toEqual([]);
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ state: "ACTIVE", email: A })));
    await loadSession(false);
    saveDraft(A, "c1", "signed in again");
    expect(loadDraft(A, "c1")).toBe("signed in again");
  });

  it("the seal holds in every tab: another tab's composer cannot write the draft back", async () => {
    seed();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
    await signOut(); // tab 1
    // Tab 2 shares localStorage but not module state: a fresh copy of the module stands in for it.
    vi.resetModules();
    const tab2 = await import("./drafts");
    tab2.saveDraft(A, "c1", "typed in the other tab");
    expect(drafts()).toEqual([]);
  });

  it("after A signs out and B signs in, A's still-open tab cannot write A's text back", async () => {
    seed();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
    await signOut(); // A, tab 1
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ state: "ACTIVE", email: B })));
    await loadSession(false); // B signs in, tab 1
    vi.resetModules();
    const tabOfA = await import("./drafts"); // A's tab, still open
    tabOfA.saveDraft(A, "c1", "A's text from the old tab");
    expect(drafts()).toEqual([]);
    saveDraft(B, "c1", "B's own");
    expect(loadDraft(B, "c1")).toBe("B's own");
  });

  it("another account signing in on the same browser finds nothing of the first", async () => {
    seed();
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ state: "ACTIVE", email: B })));
    await loadSession(false);
    expect(drafts()).toEqual([]);
    expect(loadDraft(B, "c1")).toBe("");
  });

  it("the same account keeps a fresh draft, and loses one older than a week (a browser closed on an expired session)", () => {
    const now = Date.now();
    seed(now - DRAFT_TTL_MS + 60_000);
    saveDraft(A, "c2", "older", now - DRAFT_TTL_MS - 60_000);
    purgeStaleDrafts(A, now);
    expect(loadDraft(A, "c1")).toBe("A's unsent text");
    expect(loadBaseRevision(A, "c1")).toBe(3);
    expect(loadDraft(A, null)).toBe("A's new chat");
    expect(loadDraft(A, "c2")).toBe("");
    expect(drafts().some((k) => k.includes("c2"))).toBe(false);
  });

  it("an unstamped draft (written before this policy) is treated as stale", () => {
    localStorage.setItem(`antares:draft:v1:${encodeURIComponent(A)}:chat:c9`, "legacy");
    purgeStaleDrafts(A);
    expect(loadDraft(A, "c9")).toBe("");
  });

  it("an account whose email is a prefix of another's keeps only its own", () => {
    saveDraft("a@x.test", "c1", "mine");
    saveDraft("a@x.test.evil", "c1", "theirs");
    purgeStaleDrafts("a@x.test");
    expect(loadDraft("a@x.test", "c1")).toBe("mine");
    expect(loadDraft("a@x.test.evil", "c1")).toBe("");
  });
});

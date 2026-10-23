// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "./Chat";
import { loadDraft, matchTemplates, purgeDrafts, saveDraft, TEMPLATES } from "./drafts";

beforeEach(() => localStorage.clear());
afterEach(() => localStorage.clear());

describe("drafts are keyed by account and chat", () => {
  it("survives a reload (a fresh read of storage)", () => {
    saveDraft("a@x.test", "chat-1", "half a thought");
    expect(loadDraft("a@x.test", "chat-1")).toBe("half a thought");
  });

  it("a draft written as user A is not readable as user B", () => {
    saveDraft("a@x.test", "chat-1", "A's secret");
    saveDraft("a@x.test", null, "A's new-chat draft");
    expect(loadDraft("b@x.test", "chat-1")).toBe("");
    expect(loadDraft("b@x.test", null)).toBe("");
    // An email crafted to collide with A's key still reads nothing.
    expect(loadDraft("a@x.test:chat:chat-1", "chat-1")).toBe("");
    expect(loadDraft("a@x.test", "chat-1")).toBe("A's secret");
  });

  it("a draft keyed to chat 1 does not appear in chat 2, nor in the new-chat draft", () => {
    saveDraft("a@x.test", "chat-1", "for one");
    expect(loadDraft("a@x.test", "chat-2")).toBe("");
    expect(loadDraft("a@x.test", null)).toBe("");
    saveDraft("a@x.test", "new", "a chat whose id is the word new");
    expect(loadDraft("a@x.test", null)).toBe("");
  });

  it("empty text removes the entry; purge removes every draft and nothing else", () => {
    saveDraft("a@x.test", "c", "x");
    saveDraft("b@x.test", "c", "y");
    localStorage.setItem("other", "keep");
    saveDraft("a@x.test", "c", "");
    expect(localStorage.length).toBe(2);
    purgeDrafts();
    expect(loadDraft("b@x.test", "c")).toBe("");
    expect(localStorage.getItem("other")).toBe("keep");
  });

  it("storage that throws loses the draft but never throws", () => {
    const get = Storage.prototype.getItem;
    Storage.prototype.getItem = () => {
      throw new Error("blocked");
    };
    try {
      expect(loadDraft("a@x.test", "c")).toBe("");
    } finally {
      Storage.prototype.getItem = get;
    }
  });
});

describe("slash templates", () => {
  it("lists all on a lone slash, filters by prefix, and is silent otherwise", () => {
    expect(matchTemplates("/")).toEqual([...TEMPLATES]);
    expect(matchTemplates("/SUM").map((t) => t.name)).toEqual(["summarize"]);
    expect(matchTemplates("/zzz")).toEqual([]);
    expect(matchTemplates("hello /sum")).toEqual([]);
    expect(matchTemplates("/summarize this")).toEqual([]);
    expect(matchTemplates("")).toEqual([]);
  });

  it("the composer inserts the template text and Enter picks instead of sending", () => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    const host = document.createElement("div");
    document.body.append(host);
    const changes: string[] = [];
    let sent = 0;
    const root = createRoot(host);
    act(() => root.render(<Composer value="/sum" busy={false} onChange={(v) => changes.push(v)} onSubmit={() => sent++} />));
    const ta = host.querySelector("textarea")!;
    act(() => {
      ta.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    expect(sent).toBe(0);
    expect(changes).toEqual([TEMPLATES[0].body]);
    act(() => host.querySelector<HTMLButtonElement>(".templates button")!.click());
    expect(changes).toHaveLength(2);
    act(() => root.unmount());
    host.remove();
  });
});

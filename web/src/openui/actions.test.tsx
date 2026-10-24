// @vitest-environment jsdom
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ALLOWED_ACTIONS, cleanReply, runAction } from "./actions";
import RichBlock, { onAction } from "./Block";
import { MAX_REPLY, ReplyContext } from "./reply";

afterEach(cleanup);

describe("action allowlist", () => {
  it("has exactly one action, and it only writes to the composer", () => {
    expect(ALLOWED_ACTIONS).toEqual(["reply"]);
  });

  const refused: unknown[] = ["navigate", "fetch", "/admin/users", "Reply", "reply ", "", "constructor", "__proto__", "toString", "hasOwnProperty", 5, null, undefined, {}, ["reply"], { toString: () => "reply" }];
  it.each(refused.map((n) => [JSON.stringify(n) ?? String(n), n]))("refuses a non-allowlisted action name %s and runs no handler", (_l, name) => {
    const reply = vi.fn();
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    try {
      expect(runAction(name, "hello", { reply })).toEqual({ ok: false, reason: "action not allowed" });
      expect(reply).not.toHaveBeenCalled();
      expect(fetchSpy).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("an action event coming from the renderer is refused even if it names the allowlisted action", () => {
    const reply = vi.fn();
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    try {
      for (const type of ["reply", "OpenUrl", "continue_conversation", "constructor"]) onAction({ type, params: { url: "/api/admin/users" } });
      expect(reply).not.toHaveBeenCalled();
      expect(fetchSpy).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("refuses an invalid payload or a missing composer for the allowed action", () => {
    const reply = vi.fn();
    for (const p of [5, null, undefined, {}, "", "   \u0007 ", "x".repeat(MAX_REPLY + 1)]) expect(runAction("reply", p, { reply }).ok).toBe(false);
    expect(runAction("reply", "ok", {})).toEqual({ ok: false, reason: "no composer" });
    expect(reply).not.toHaveBeenCalled();
  });
});

describe("model-supplied action payload and DOM sinks", () => {
  const EVIL = '<img src=x onerror="window.__pwned=1"><script>window.__pwned=2</script>‮';

  it("is rebuilt as inert text: controls and bidi overrides are stripped, markup is kept as characters only", () => {
    expect(cleanReply(`a\u0000b‮c⁦d\u009Fe\nf\tg`)).toBe("abcde\nf\tg");
    expect(cleanReply(`  ${EVIL}  `)).toBe('<img src=x onerror="window.__pwned=1"><script>window.__pwned=2</script>');
  });

  it("reaches only the composer sink and never creates an element", () => {
    const composer = document.createElement("textarea");
    document.body.append(composer);
    expect(runAction("reply", EVIL, { reply: (t) => (composer.value += t) }).ok).toBe(true);
    expect(composer.value).toContain("<img");
    expect(document.querySelector("img, script, iframe")).toBeNull();
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined();
    composer.remove();
  });

  it("a button carrying markup renders and acts as text only", () => {
    const add = vi.fn();
    const { container } = render(
      <ReplyContext.Provider value={add}>
        <RichBlock source={`root = Stack([Button("<b>go</b>", ${JSON.stringify(EVIL.replace(/‮/g, ""))})])`} />
      </ReplyContext.Provider>,
    );
    fireEvent.click(screen.getByRole("button"));
    expect(add).toHaveBeenCalledTimes(1);
    expect(container.querySelector("b, img, script")).toBeNull();
    expect(document.querySelector("img, script")).toBeNull();
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined();
  });

  it("no module of the renderer uses a DOM sink or dynamic code", () => {
    const here = __dirname;
    const files = [...readdirSync(here).filter((f) => /\.tsx?$/.test(f) && !/\.test\./.test(f)).map((f) => join(here, f)), join(here, "..", "MessageBody.tsx")];
    const sink = /dangerouslySetInnerHTML|\.innerHTML|\.outerHTML|insertAdjacentHTML|document\.write|\beval\s*\(|new Function|createContextualFragment|srcDoc|\bfetch\s*\(|\bXMLHttpRequest\b|\bsendBeacon\b|window\.open|location\.(href|assign|replace)|\bimport\s*\(\s*[^"'\s]/;
    for (const f of files) expect(readFileSync(f, "utf8").replace(/\/\/.*$/gm, ""), f).not.toMatch(sink);
  });

  it("the composer path in Chat.tsx (addToDraft) has no DOM sink or dynamic code either", () => {
    // Chat.tsx legitimately calls the API, so only DOM sinks and code execution are scanned here.
    const domSink = /dangerouslySetInnerHTML|\.innerHTML|\.outerHTML|insertAdjacentHTML|document\.write|\beval\s*\(|new Function|createContextualFragment|srcDoc|window\.open/;
    expect(readFileSync(join(__dirname, "..", "Chat.tsx"), "utf8").replace(/\/\/.*$/gm, "")).not.toMatch(domSink);
  });
});

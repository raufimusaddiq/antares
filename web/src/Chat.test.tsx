import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Composer, Transcript } from "./Chat";
import { Markdown, safeHref } from "./Markdown";
import { describeCode, getMessages, listChats, sendMessage } from "./chatApi";
import { isTerminal, newRun, parseEvent, reduce, runNotice, type LiveRun, type RunEvent } from "./runState";

const md = (text: string) => renderToStaticMarkup(<Markdown text={text} />);

describe("Markdown has no path to raw HTML", () => {
  it("shows markup as text", () => {
    const html = md('<script>alert(1)</script> <img src=x onerror=alert(1)> <a href="javascript:alert(1)">x</a>');
    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<a ");
    expect(html).toContain("&lt;script&gt;");
  });

  it("keeps only absolute http, https and mailto links, and hardens them", () => {
    const ok = md("[site](https://example.test/a?b=1)");
    expect(ok).toContain('href="https://example.test/a?b=1"');
    expect(ok).toContain('rel="noopener noreferrer nofollow"');
    expect(ok).toContain('target="_blank"');
    for (const bad of ["javascript:alert(1)", "JaVaScRiPt:alert(1)", "java\tscript:alert(1)", "data:text/html;base64,AAAA", "vbscript:x", "//evil.test/", "/relative", "file:///etc/passwd", " https://example.test", "https://example.test/ x"]) {
      expect(safeHref(bad), bad).toBeNull();
      expect(md(`[x](${bad})`), bad).not.toContain("<a ");
    }
  });

  it("does not render images or embed anything", () => {
    const html = md("![alt](https://example.test/p.png)\n\n<iframe src=x></iframe>");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<iframe");
  });

  it("renders the supported subset", () => {
    const html = md("# Title\n\nSome **bold**, *italic* and `code`.\n\n- one\n- two\n\n1. a\n2. b\n\n> quoted\n\n```\nlet x = <b>1</b>\n```");
    for (const frag of ["<h3>Title</h3>", "<strong>bold</strong>", "<em>italic</em>", "<code>code</code>", "<ul><li>one</li><li>two</li></ul>", "<ol><li>a</li><li>b</li></ol>", "<blockquote>", "&lt;b&gt;1&lt;/b&gt;"]) {
      expect(html).toContain(frag);
    }
    expect(md("```\nunterminated <b>")).toContain("&lt;b&gt;");
  });

  it("falls back to plain text for huge input and never throws on odd input", () => {
    expect(md("a".repeat(250_000))).toContain('class="plain"');
    for (const s of ["", "**", "[", "[]()", "[x](", "`", "***a***", "- ", "#", ">", "\u0000", "\r\n\r\n"]) {
      expect(() => md(s), JSON.stringify(s)).not.toThrow();
    }
  });
});

const ev = (type: RunEvent["type"], extra: object = {}) => ({ type, ...extra }) as RunEvent;
const play = (events: RunEvent[], from: LiveRun = newRun("r1")) => events.reduce(reduce, from);

describe("run state tells the truth", () => {
  it("streams deltas in order and completes only on run.completed", () => {
    const mid = play([ev("run.started"), ev("assistant.delta", { text: "Hel" }), ev("assistant.delta", { text: "lo" })]);
    expect(mid.phase).toBe("streaming");
    expect(mid.text).toBe("Hello");
    const done = reduce(mid, ev("run.completed", { output: "", status: "COMPLETED" }));
    expect(done.phase).toBe("completed");
    expect(done.text).toBe("Hello");
    expect(reduce(mid, ev("run.completed", { output: "Final", status: "COMPLETED" })).text).toBe("Final");
  });

  it("a failed, cancelled or timed-out run is never completed, and a terminal run never changes", () => {
    const failed = play([ev("assistant.delta", { text: "half" }), ev("run.failed", { code: "RUN_FAILED", status: "FAILED" })]);
    expect(failed.phase).toBe("failed");
    expect(runNotice(failed)).toMatch(/did not finish/);
    expect(play([ev("run.failed", { code: "UPSTREAM_TIMEOUT", status: "TIMED_OUT" })]).timedOut).toBe(true);
    const cancelled = play([ev("run.cancelled")]);
    for (const terminal of [failed, cancelled]) {
      for (const later of [ev("run.completed", { output: "x", status: "COMPLETED" }), ev("assistant.delta", { text: "more" }), ev("run.started"), ev("run.disconnected"), ev("stream.lost")]) {
        const after = reduce(terminal, later);
        expect(after).toBe(terminal);
        expect(isTerminal(after.phase)).toBe(true);
      }
    }
    expect(runNotice(cancelled)).toMatch(/Cancelled/);
  });

  it("a lost connection is shown as lost, not as finished", () => {
    const lost = play([ev("assistant.delta", { text: "x" }), ev("run.disconnected")]);
    expect(lost.phase).toBe("disconnected");
    expect(lost.phase).not.toBe("completed");
    expect(runNotice(lost)).toMatch(/lost/);
    expect(play([ev("stream.lost")]).phase).toBe("reconnecting");
    // and it recovers into streaming, not completion
    expect(reduce(lost, ev("stream.open")).phase).toBe("streaming");
  });

  it("run.resumed clears text so a replay cannot duplicate it", () => {
    const s = play([ev("assistant.delta", { text: "abc" }), ev("run.resumed"), ev("assistant.delta", { text: "abcd" })]);
    expect(s.text).toBe("abcd");
  });

  it("a cancel Hermes did not confirm says the run may still be going", () => {
    const c = { ...play([ev("run.cancelled")]), maybeStillRunning: true };
    expect(runNotice(c)).toMatch(/may still be working/);
  });

  it("tracks tools by id", () => {
    const s = play([ev("tool.started", { id: "t1", label: "Web search" }), ev("tool.completed", { id: "t1", summary: "3 results" })]);
    expect(s.tools).toEqual([{ id: "t1", label: "Web search", done: true, summary: "3 results" }]);
  });
});

describe("parseEvent drops what it does not understand", () => {
  it("parses the normalized events", () => {
    expect(parseEvent("assistant.delta", '{"text":"hi"}')).toEqual({ type: "assistant.delta", text: "hi" });
    expect(parseEvent("run.cancelled", '{"runId":"r","status":"CANCELLED"}')).toEqual({ type: "run.cancelled" });
    expect(parseEvent("run.failed", '{"status":"TIMED_OUT","code":"UPSTREAM_TIMEOUT"}')).toEqual({ type: "run.failed", code: "UPSTREAM_TIMEOUT", status: "TIMED_OUT" });
  });
  it("accepts completion only with an explicit COMPLETED status", () => {
    expect(parseEvent("run.completed", '{"status":"COMPLETED","output":"x"}')?.type).toBe("run.completed");
    for (const d of ['{}', '{"status":"FAILED"}', '{"status":"completed"}', "not json", "[]", "null"]) {
      expect(parseEvent("run.completed", d), d).toBeNull();
    }
  });
  it("ignores unknown names and malformed deltas", () => {
    expect(parseEvent("run.surprise", "{}")).toBeNull();
    expect(parseEvent("assistant.delta", '{"text":5}')).toBeNull();
    expect(parseEvent("assistant.delta", "oops")).toBeNull();
  });
});

describe("transcript and composer markup", () => {
  const noop = () => {};
  const props = { onCancel: noop, onRetry: noop, onReconnect: noop, onResend: noop };

  it("escapes user and assistant text", () => {
    const html = renderToStaticMarkup(
      <Transcript {...props} run={null} pending={null} turns={[{ seq: 0, role: "user", text: "<img src=x onerror=1>" }, { seq: 1, role: "assistant", text: "<script>1</script>" }]} />,
    );
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<script");
  });

  it("shows a failed run with its partial text, a notice and a retry, not as a completed answer", () => {
    const run = play([ev("assistant.delta", { text: "half an ans" }), ev("run.failed", { code: "", status: "FAILED" })]);
    const html = renderToStaticMarkup(<Transcript {...props} turns={[]} pending={null} run={run} />);
    expect(html).toContain("Partial response");
    expect(html).toContain("did not finish");
    expect(html).toContain("Try again");
    expect(html).not.toContain(">Cancel<");
  });

  it("offers Cancel while streaming and Reconnect when disconnected", () => {
    const live = renderToStaticMarkup(<Transcript {...props} turns={[]} pending={null} run={newRun("r")} />);
    expect(live).toContain(">Cancel<");
    expect(live).toContain("Working…");
    const lost = renderToStaticMarkup(<Transcript {...props} turns={[]} pending={null} run={play([ev("run.disconnected")])} />);
    expect(lost).toContain(">Reconnect<");
    expect(lost).toContain("Connection to the assistant was lost");
  });

  it("an unsent message says so and can be sent again", () => {
    const html = renderToStaticMarkup(<Transcript {...props} turns={[]} run={null} pending={{ input: "hi", key: "k", failure: "Could not reach Antares." }} />);
    expect(html).toContain("Not sent");
    expect(html).toContain("Send again");
  });

  it("the composer is disabled while empty or busy", () => {
    expect(renderToStaticMarkup(<Composer value="" busy={false} onChange={noop} onSubmit={noop} />)).toContain("disabled");
    expect(renderToStaticMarkup(<Composer value="hi" busy={true} onChange={noop} onSubmit={noop} />)).toContain("disabled");
    expect(renderToStaticMarkup(<Composer value="hi" busy={false} onChange={noop} onSubmit={noop} />)).not.toContain("disabled");
  });
});

describe("api client", () => {
  afterEach(() => vi.unstubAllGlobals());
  const reply = (status: number, body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status }));

  it("sends only cookies, the message and the idempotency key", async () => {
    const f = reply(202, { runId: "r1", status: "PENDING" });
    vi.stubGlobal("fetch", f);
    const r = await sendMessage("c1", "hello", "web-key-1");
    expect(r).toEqual({ ok: true, value: { runId: "r1", status: "PENDING" } });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/chats/c1/messages");
    expect(init.credentials).toBe("same-origin");
    expect(JSON.parse(init.body as string)).toEqual({ input: "hello" });
    expect(Object.keys(init.headers as object).map((k) => k.toLowerCase()).sort()).toEqual(["accept", "content-type", "idempotency-key"]);
  });

  it("maps an error envelope to its code and never shows the server's message", async () => {
    vi.stubGlobal("fetch", reply(409, { error: { code: "RUN_CONFLICT", message: "SECRET internal text", requestId: "req_1" } }));
    const r = await sendMessage("c1", "x", "web-key-1");
    expect(r).toEqual({ ok: false, code: "RUN_CONFLICT", status: 409 });
    expect(describeCode("RUN_CONFLICT")).not.toContain("SECRET");
  });

  it("treats an unknown code, a network failure and an unreadable success as failures", async () => {
    vi.stubGlobal("fetch", reply(500, { error: { code: "WHATEVER" } }));
    expect(await listChats()).toMatchObject({ ok: false, code: "HERMES_UNAVAILABLE" });
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("down"))));
    expect(await listChats()).toMatchObject({ ok: false, code: "NETWORK" });
    vi.stubGlobal("fetch", reply(200, { nope: true }));
    expect(await listChats()).toMatchObject({ ok: false });
    vi.stubGlobal("fetch", reply(200, { messages: "x" }));
    expect(await getMessages("c1")).toMatchObject({ ok: false });
  });

  it("keeps only user and assistant turns, with their positions and the next cursor", async () => {
    vi.stubGlobal("fetch", reply(200, { messages: [{ seq: 4, role: "user", text: "a" }, { seq: 5, role: "tool", text: "t" }, { seq: 6, role: "assistant", text: "b" }], nextCursor: "c2", total: 7 }));
    const r = await getMessages("c1");
    expect(r).toEqual({ ok: true, value: { turns: [{ seq: 4, role: "user", text: "a" }, { seq: 6, role: "assistant", text: "b" }], nextCursor: "c2", total: 7 } });
  });

  it("asks for an older page by cursor and nothing else", async () => {
    const f = reply(200, { messages: [], nextCursor: "" });
    vi.stubGlobal("fetch", f);
    await getMessages("c1", "a b/c+d");
    expect((f.mock.calls[0] as unknown as [string])[0]).toBe("/api/chats/c1/messages?cursor=a%20b%2Fc%2Bd");
    await getMessages("c1");
    expect((f.mock.calls[1] as unknown as [string])[0]).toBe("/api/chats/c1/messages");
  });

  it("refuses a page whose turns cannot be placed or are out of order, rather than guessing", async () => {
    for (const messages of [[{ role: "user", text: "no seq" }], [{ seq: -1, role: "user", text: "x" }], [{ seq: 1.5, role: "user", text: "x" }], [{ seq: 3, role: "user", text: "x" }, { seq: 3, role: "assistant", text: "y" }], [{ seq: 5, role: "user", text: "x" }, { seq: 4, role: "assistant", text: "y" }]]) {
      vi.stubGlobal("fetch", reply(200, { messages, nextCursor: "" }));
      expect(await getMessages("c1"), JSON.stringify(messages)).toMatchObject({ ok: false });
    }
  });

  it("maps a changed history to its own code", async () => {
    vi.stubGlobal("fetch", reply(409, { error: { code: "HISTORY_CHANGED", message: "x" } }));
    expect(await getMessages("c1", "stale")).toEqual({ ok: false, code: "HISTORY_CHANGED", status: 409 });
    expect(describeCode("HISTORY_CHANGED")).toMatch(/changed/);
  });

  it("lists chats with a cursor", async () => {
    const f = reply(200, { chats: [{ id: "a", title: "t", createdAt: "", updatedAt: "", activeRunId: "" }], nextCursor: "n1" });
    vi.stubGlobal("fetch", f);
    expect(await listChats("p 1")).toMatchObject({ ok: true, value: { nextCursor: "n1" } });
    expect((f.mock.calls[0] as unknown as [string])[0]).toBe("/api/chats?cursor=p%201");
  });
});

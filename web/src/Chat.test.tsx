import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Composer, Transcript } from "./Chat";
import { parseInventory } from "./chatApi";
import { Markdown, safeHref } from "./Markdown";
import { indentInFence, wrapSelection } from "./composerEdit";
import { describeCode, describeGlance, describeSpending, describeTurns, describeUsage, parseSpending, reconcileSpending, spendingFields, getMessages, listChats, parseLimitInput, parseUsage, sendMessage } from "./chatApi";
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

  it("renders a fenced block with its language, and a hostile language is dropped", () => {
    const html = md("```ts\nconst a = 1;\n```");
    expect(html).toContain('data-lang="ts"');
    expect(html).toContain(">ts</span>");
    expect(html).toContain("Copy");
    const bad = md('```"onmouseover="x\nhi\n```');
    expect(bad).not.toContain("onmouseover=");
    expect(bad).not.toContain("data-lang");
  });

  it("renders tables with inert cells", () => {
    const html = md("| a | b |\n|---|:-:|\n| 1 | **2** |");
    expect(html).toContain("<th>a</th>");
    expect(html).toContain("<td><strong>2</strong></td>");
  });

  it("falls back to plain text for huge input and never throws on odd input", () => {
    expect(md("a".repeat(250_000))).toContain('class="plain"');
    for (const s of ["", "**", "[", "[]()", "[x](", "`", "***a***", "- ", "#", ">", "\u0000", "\r\n\r\n"]) {
      expect(() => md(s), JSON.stringify(s)).not.toThrow();
    }
  });
});

describe("composer shortcuts", () => {
  it("wraps a selection and indents only inside a fence", () => {
    expect(wrapSelection("hi there", 3, 8, "b")).toEqual({ value: "hi **there**", start: 5, end: 10 });
    expect(wrapSelection("x", 1, 1, "e")?.value).toBe("x``");
    expect(wrapSelection("x", 0, 1, "z")).toBeNull();
    expect(indentInFence("```\nab", 6, 6)?.value).toBe("```\nab  ");
    expect(indentInFence("plain", 2, 2)).toBeNull();
    // A non-empty selection inside a fence must never lose its text.
    const sel = "```\nlet x = 1;\n";
    const text = sel + "```";
    expect(indentInFence(text, 4, 14)).toBeNull();
    const after = indentInFence("```\nabcd", 5, 7);
    expect(after === null || after.value.includes("bc")).toBe(true);
    expect(indentInFence("```\nx\n```\nafter", 15, 15)).toBeNull();
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
      for (const later of [ev("run.completed", { output: "x", status: "COMPLETED" }), ev("assistant.delta", { text: "more" }), ev("run.started"), ev("run.disconnected"), ev("stream.closed"), ev("stream.lost")]) {
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

  it("a dropped stream preserves the active response and can reconnect", () => {
    const dropped = play([ev("assistant.delta", { text: "partial" }), ev("stream.closed")]);
    expect(dropped.phase).toBe("detached");
    expect(isTerminal(dropped.phase)).toBe(false);
    expect(dropped.text).toBe("partial");
    expect(runNotice(dropped)).toMatch(/run is still active/);
    expect(reduce(dropped, ev("stream.open")).phase).toBe("streaming");
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
    const s = play([ev("tool.started", { id: "t1", label: "Web search" }), ev("tool.completed", { id: "t1", summary: "3 results", failed: false, durationMs: 1200 })]);
    expect(s.tools).toEqual([{ id: "t1", label: "Web search", state: "done", summary: "3 results", durationMs: 1200 }]);
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

  it("renders regenerate, edit and rewind disabled, each with the server's reason, and never as an action", () => {
    const row = (id: string, reason: string) => ({ id, label: id, status: "unsupported", reason });
    const inv = parseInventory({
      capabilities: [row("regenerate", "no retry route"), row("edit_turn", "no branch route"), row("rewind", "no truncate route")],
      toolsets: [], providers: [], skills: [],
    });
    const turns = [{ seq: 0, role: "user" as const, text: "q" }, { seq: 1, role: "assistant" as const, text: "a" }];
    const html = renderToStaticMarkup(<Transcript {...props} chatId="c1" inventory={inv} run={null} pending={null} turns={turns} />);
    for (const [k, label, reason] of [["regenerate", "Regenerate", "no retry route"], ["edit", "Edit", "no branch route"], ["rewind", "Rewind", "no truncate route"]]) {
      const btn = html.match(new RegExp(`<button[^>]*data-control="${k}"[^>]*>${label}</button>`))?.[0] ?? "";
      expect(btn, k).toContain('aria-disabled="true"');
      expect(btn, k).toMatch(/aria-describedby="tc-/);
      expect(btn, k).not.toContain("onclick");
      expect(html, k).toContain(`>${reason}</small>`);
    }
    // No capability answer: still disabled, and says the availability is unknown.
    const unknown = renderToStaticMarkup(<Transcript {...props} chatId="c1" run={null} pending={null} turns={turns} />);
    expect(unknown.match(/aria-disabled="true"/g)).toHaveLength(3);
    expect(unknown.match(/Availability unknown/g)).toHaveLength(3);
    // A server that says "available" does not make a button that does nothing look usable.
    const avail = parseInventory({ capabilities: [{ id: "rewind", label: "r", status: "available" }], toolsets: [], providers: [], skills: [] });
    const a = renderToStaticMarkup(<Transcript {...props} chatId="c1" inventory={avail} run={null} pending={null} turns={turns} />);
    expect(a).toContain("Not available in this app yet");
    // Without a chat nothing is rendered.
    expect(renderToStaticMarkup(<Transcript {...props} run={null} pending={null} turns={turns} />)).not.toContain("data-control");
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

describe("usage text", () => {
  const base = { tokens: { input: 1000, output: 200, cacheRead: 400, cacheWrite: 0, total: 1200 }, finishedRuns: 2, reportedRuns: 1, costIDR: 3.4, unpricedRuns: 0, unknownModelRuns: 0, turns: [] as { runId: string; status: string; tokens: { input: number; output: number; total: number } | null }[], turnsTruncated: false, priceListDate: "2026-10-23", windowTokens: 1000000, carriedTokens: null as number | null, spending: null, budget: { limit: 2000000 as number | null, used: 1200 as number | null, enforced: true, reached: false, basis: "" } };

  it("labels cost as an estimate with its price date, and shows unknowns as a dash", () => {
    const t = describeUsage(base).join("\n");
    expect(t).toContain("Estimated cost: about Rp 3");
    expect(t).toContain("2026-10-23");
    expect(t).toContain("Context in use now: —");
    expect(t).toContain("Model context window: 1,000,000 tokens");
    expect(t).toContain("Used in this chat, all replies: 1,200 tokens");
    expect(t).not.toMatch(/\d+%/);
    expect(t).toContain("1 finished reply reported no usage and is not counted");
    expect(describeUsage({ ...base, unpricedRuns: 2 }).join("\n")).toContain("2 replies were from a model with no known price");
    expect(describeUsage({ ...base, tokens: null, costIDR: null }).join("\n")).toContain("Used in this chat, all replies: — (none reported)");
  });

  it("keeps an unknown occupancy unknown: a stray number without REPORTED is not read, and nothing becomes 0% (#165)", () => {
    const wire = (context: object) =>
      parseUsage({ chatId: "c", tokens: null, runs: { finished: 0, reported: 0 }, context, cost: { amount: null }, budget: { limitTokens: null, usedTokens: null } });
    expect(wire({ windowTokens: 128000, occupancy: "UNKNOWN", carriedTokens: null })?.carriedTokens).toBeNull();
    expect(wire({ windowTokens: 128000, occupancy: "UNKNOWN", carriedTokens: 0 })?.carriedTokens).toBeNull();
    expect(wire({ windowTokens: 128000, carriedTokens: 5000 })?.carriedTokens).toBeNull();
    const reported = wire({ windowTokens: 100000, occupancy: "REPORTED", carriedTokens: 25000 })!;
    expect(reported.carriedTokens).toBe(25000);
    expect(describeUsage(reported).join("\n")).toContain("Context in use now: 25,000 tokens (25% of the window)");
    const unknown = describeUsage(wire({ windowTokens: 128000, occupancy: "UNKNOWN", carriedTokens: null })!).join("\n");
    expect(unknown).toContain("Context in use now: —");
    expect(unknown).not.toMatch(/0%|: 0 tokens/);
  });

  it("the usage chip is glanceable and keeps unknown context unknown (#164)", () => {
    expect(describeGlance(base)).toBe("1,200 tokens used · context in use unknown");
    expect(describeGlance({ ...base, tokens: null })).toBe("Usage unknown · context in use unknown");
    expect(describeGlance({ ...base, carriedTokens: 250000 })).toBe("1,200 tokens used · context 25% full");
    expect(describeGlance({ ...base, budget: { ...base.budget, reached: true } })).toBe("Token limit reached");
    expect(describeGlance(base)).not.toMatch(/0%/);
  });

  it("spending: estimates in rupiah, unknown never zero, warnings never block, the hard limit says it does (#152)", () => {
    const sp = parseSpending({ currency: "IDR", day: { spent: 5500, unpricedRuns: 1 }, month: { spent: null, unpricedRuns: 2 }, dailySoft: 1000, monthlySoft: null, dailyHard: 5500, warnings: ["daily"], hardReached: true })!;
    const t = describeSpending(sp).join("\n");
    expect(t).toContain("Estimated spend today (UTC): about Rp 5,500, plus 1 reply with no known price (not counted)");
    expect(t).toContain("Estimated spend this month: — (no priced reply)");
    expect(t).toContain("Daily warning at Rp 1,000: reached (a warning only; messages still go through)");
    expect(t).toContain("Daily spending limit of Rp 5,500 reached: new messages are refused");
    expect(t).not.toMatch(/Rp 0\b/);
    expect(describeGlance({ ...base, spending: sp })).toBe("Daily spending limit reached");
    expect(describeGlance({ ...base, spending: { ...sp, hardReached: false, warnings: ["daily", "monthly"] } })).toBe("Spending warning: daily and monthly limit reached");
    expect(describeSpending({ ...sp, dailySoft: null, monthlySoft: null, dailyHard: null, warnings: [], hardReached: false }).join("\n")).toContain("No spending limits set");
    expect(parseSpending({ day: {}, month: {} })?.day.spent).toBeNull();
    expect(parseSpending(null)).toBeNull();
  });

  it("spending form: a refresh updates untouched fields and keeps unsaved edits (#152)", () => {
    const before = spendingFields({ dailySoft: 1000, monthlySoft: null, dailyHard: 5500.4 });
    expect(before).toEqual({ dailySoft: "1000", monthlySoft: "", dailyHard: "5500" });
    const after = spendingFields({ dailySoft: 2000, monthlySoft: 9000, dailyHard: null });
    // The user is typing a new daily limit; the other two are as the server last said.
    const typed = { ...before, dailyHard: "7000" };
    expect(reconcileSpending(before, after, typed)).toEqual({ dailySoft: "2000", monthlySoft: "9000", dailyHard: "7000" });
    expect(reconcileSpending(before, after, before)).toEqual(after);
    expect(reconcileSpending(after, after, typed)).toEqual(typed);
  });

  it("states the limit, its basis and what happens at it", () => {
    const t = describeUsage(base).join("\n");
    expect(t).toContain("across all your chats");
    expect(t).toContain("input includes cache reads");
    expect(t).toContain("At the limit the chat stops taking new messages");
    expect(describeUsage({ ...base, budget: { ...base.budget, used: 2000000, reached: true } }).join("\n")).toContain("raise or remove the limit");
    expect(describeCode("BUDGET_REACHED")).toContain("token limit");
  });

  it("has no limit by default, and never prints unknown usage as zero", () => {
    expect(describeUsage({ ...base, budget: { ...base.budget, limit: null, enforced: false } }).join("\n")).toContain("none set");
    const t = describeUsage({ ...base, tokens: null, budget: { ...base.budget, used: null, enforced: false } }).join("\n");
    expect(t).toContain("usage is unknown");
    expect(t).toContain("not being applied");
    expect(t).not.toContain("0 of");
  });

  it("refuses a limit that is not a whole number in range instead of sending it", () => {
    expect(parseLimitInput("")).toBeNull();
    expect(parseLimitInput("50,000")).toBe(50000);
    for (const bad of ["abc", "5.5", "-20000", "9999", "1000000001", "1e5", "NaN", "0x10000"]) expect(parseLimitInput(bad)).toBeUndefined();
  });

  it("says no cost is estimated for polled replies, and lists each reply", () => {
    expect(describeUsage({ ...base, unpricedRuns: 1, unknownModelRuns: 1, costIDR: null }).join("\n")).toContain("did not report which model answered");
    const turns = [{ runId: "a", status: "COMPLETED", tokens: { input: 5, output: 1, total: 6 } }, { runId: "b", status: "COMPLETED", tokens: null }];
    expect(describeTurns({ ...base, turns })).toEqual(["Reply 1: 6 tokens (5 in, 1 out)", "Reply 2: — (no usage reported)"]);
  });

  it("keeps cache writes and nulls from the server", () => {
    const p = parseUsage({ tokens: { input: 5, output: 1, cacheRead: 2, cacheWrite: 3, total: 6 }, runs: { finished: 1, reported: 1 }, cost: {}, context: {}, budget: { limitTokens: null, usedTokens: 6, enforced: false, reached: false, basis: "x" } });
    expect(p?.tokens?.cacheWrite).toBe(3);
    expect(p?.budget.limit).toBeNull();
    expect(parseUsage({ runs: { finished: 0, reported: 0 }, cost: {}, context: {}, budget: { limitTokens: 5, usedTokens: null } })?.budget.used).toBeNull();
  });

  it("refuses a usage body it cannot read instead of guessing", () => {
    expect(parseUsage({})).toBeNull();
    expect(parseUsage({ runs: {}, cost: {}, context: {}, budget: { limitTokens: -1, usedTokens: 0 } })).toBeNull();
  });
});

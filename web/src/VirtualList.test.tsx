import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Transcript } from "./Chat";
import { VirtualList, WINDOW_ABOVE } from "./VirtualList";
import type { Turn } from "./chatApi";

const turns = (n: number): Turn[] => Array.from({ length: n }, (_, i) => ({ seq: i, role: i % 2 ? "assistant" : "user", text: `message ${i}` }));
const noop = () => {};
const props = { pending: null, run: null, onCancel: noop, onRetry: noop, onReconnect: noop, onResend: noop };
const mounted = (html: string) => (html.match(/data-vkey=/g) ?? []).length;

describe("the conversation is windowed", () => {
  it("mounts a bounded number of turns for a 1,000-turn conversation, ending at the newest", () => {
    const html = renderToStaticMarkup(<Transcript {...props} turns={turns(1000)} />);
    expect(mounted(html)).toBeGreaterThan(0);
    expect(mounted(html)).toBeLessThan(40);
    expect(html).toContain("message 999");
    expect(html).not.toContain("message 0<");
    expect(html).toContain('aria-setsize="1000"');
  });

  it("mounts every turn of an ordinary conversation, so selection and find work", () => {
    expect(mounted(renderToStaticMarkup(<Transcript {...props} turns={turns(WINDOW_ABOVE)} />))).toBe(WINDOW_ABOVE);
  });

  it("says where the history ends or continues", () => {
    expect(renderToStaticMarkup(<Transcript {...props} turns={turns(3)} />)).toContain("Start of the conversation");
    expect(renderToStaticMarkup(<Transcript {...props} turns={turns(3)} hasMore />)).toContain("Scroll up for older messages");
    expect(renderToStaticMarkup(<Transcript {...props} turns={turns(3)} hasMore loadingMore />)).toContain("Loading older messages");
    const failed = renderToStaticMarkup(<Transcript {...props} turns={turns(3)} hasMore olderError="The assistant is unavailable." />);
    expect(failed).toContain("Older messages unavailable");
    expect(failed).toContain(">Retry<");
  });

  it("is an accessible feed with a focusable scroll area", () => {
    const html = renderToStaticMarkup(<VirtualList items={["a"]} itemKey={(x) => x} render={(x) => <p>{x}</p>} label="Conversation" hasMore={false} loadingMore={false} topNote="" onLoadMore={noop} />);
    expect(html).toContain('role="feed"');
    expect(html).toContain('tabindex="0"');
    expect(html).toContain('aria-label="Conversation"');
  });
});

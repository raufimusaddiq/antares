// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Composer, Transcript } from "./Chat";

afterEach(cleanup);

function Host({ initial, onSubmit }: { initial: string; onSubmit: () => void }) {
  const [v, setV] = useState(initial);
  return <Composer value={v} busy={false} onChange={setV} onSubmit={onSubmit} />;
}
const box = () => screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
const at = (el: HTMLTextAreaElement, s: number, e = s) => el.setSelectionRange(s, e);

describe("composer keys (UXP-005)", () => {
  it("Enter sends, Shift+Enter does not", () => {
    const send = vi.fn();
    render(<Host initial="hi" onSubmit={send} />);
    expect(fireEvent.keyDown(box(), { key: "Enter", shiftKey: true })).toBe(true);
    expect(send).not.toHaveBeenCalled();
    expect(fireEvent.keyDown(box(), { key: "Enter" })).toBe(false);
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("an IME Enter never sends: isComposing, keyCode 229, and between composition events", () => {
    const send = vi.fn();
    render(<Host initial="にほん" onSubmit={send} />);
    fireEvent.keyDown(box(), { key: "Enter", isComposing: true });
    fireEvent.keyDown(box(), { key: "Enter", keyCode: 229 }); // Safari: after compositionend
    fireEvent.compositionStart(box());
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(send).not.toHaveBeenCalled();
    fireEvent.compositionEnd(box());
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("Tab indents inside a fence; Escape then Tab, and Shift+Tab, leave it; outside a fence Tab is untouched", () => {
    render(<Host initial={"```\nx"} onSubmit={() => {}} />);
    at(box(), 5);
    expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(false);
    expect(box().value).toBe("```\nx  ");
    expect(fireEvent.keyDown(box(), { key: "Tab", shiftKey: true })).toBe(true);
    fireEvent.keyDown(box(), { key: "Escape" });
    expect(screen.getByRole("status").textContent).toContain("Tab now moves focus");
    expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(true); // browser moves focus
    expect(box().value).toBe("```\nx  ");
    expect(screen.getByRole("status").textContent).toBe("");
    expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(false); // indent is armed again
    cleanup();
    render(<Host initial="plain" onSubmit={() => {}} />);
    at(box(), 5);
    expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(true);
  });

  it("Ctrl+Shift+8 makes a list and Ctrl+Shift+E a fence", () => {
    render(<Host initial={"a\nb"} onSubmit={() => {}} />);
    at(box(), 0, 3);
    fireEvent.keyDown(box(), { key: "*", code: "Digit8", ctrlKey: true, shiftKey: true });
    expect(box().value).toBe("- a\n- b");
    cleanup();
    render(<Host initial="code" onSubmit={() => {}} />);
    at(box(), 0, 4);
    fireEvent.keyDown(box(), { key: "E", code: "KeyE", ctrlKey: true, shiftKey: true });
    expect(box().value).toBe("```\ncode\n```");
  });

  it("describes the keys to assistive technology", () => {
    render(<Host initial="" onSubmit={() => {}} />);
    expect(box().getAttribute("aria-describedby")).toBe("composer-hint");
    expect(document.getElementById("composer-hint")!.textContent).toContain("Escape then Tab");
  });
});

describe("user turns", () => {
  it("render Markdown and code as inert text", () => {
    const text = "**hi** `x`\n\n```html\n<img src=x onerror=alert(1)>\n```";
    const { container } = render(
      <Transcript turns={[{ seq: 0, role: "user", text }]} pending={null} run={null} chatId="c" onResend={() => {}} onCancel={() => {}} onRetry={() => {}} onReconnect={() => {}} onLoadMore={() => {}} hasMore={false} loadingMore={false} olderError="" focusSeq={null} focusNonce={0} />,
    );
    expect(container.querySelector("strong")?.textContent).toBe("hi");
    expect(container.querySelector("pre code")?.textContent).toBe("<img src=x onerror=alert(1)>");
    expect(container.querySelector("img")).toBeNull();
  });
});

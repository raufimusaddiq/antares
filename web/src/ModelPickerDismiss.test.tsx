// @vitest-environment jsdom
// The open model picker floats over the conversation: Escape or a press outside closes it; a press inside does not.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render } from "@testing-library/react";
import { ModelPicker } from "./ModelPicker";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("model picker dismissal", () => {
  it("closes on Escape and on a press outside, stays open on a press inside", () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })));
    const { container } = render(<div><ModelPicker chatId="c1" disabled={false} /><p id="outside">text</p></div>);
    const d = container.querySelector<HTMLDetailsElement>("details.model-picker")!;
    d.open = true;
    fireEvent.pointerDown(d.querySelector(".model-menu")!);
    expect(d.open).toBe(true);
    fireEvent.pointerDown(container.querySelector("#outside")!);
    expect(d.open).toBe(false);
    d.open = true;
    fireEvent.keyDown(document, { key: "Escape" });
    expect(d.open).toBe(false);
    expect(document.activeElement).toBe(d.querySelector("summary"));
  });
});

import { describe, expect, it } from "vitest";
import { fenceSelection, indentInFence, inFence, toggleList, wrapSelection } from "./composerEdit";

describe("composer Markdown edits", () => {
  it("wraps a selection and an empty caret", () => {
    expect(wrapSelection("a bc d", 2, 4, "b")).toEqual({ value: "a **bc** d", start: 4, end: 6 });
    expect(wrapSelection("x", 1, 1, "e")).toEqual({ value: "x``", start: 2, end: 2 });
  });

  it("toggles a bullet on every selected line, and back", () => {
    const on = toggleList("one\ntwo\nthree", 0, 7)!;
    expect(on.value).toBe("- one\n- two\nthree");
    const off = toggleList(on.value, on.start, on.end)!;
    expect(off.value).toBe("one\ntwo\nthree");
  });

  it("a selection that ends at a line start does not bullet the next line", () => {
    expect(toggleList("a\nb", 0, 2)!.value).toBe("- a\nb");
  });

  it("offers neither a bullet nor a nested fence inside a code fence", () => {
    const v = "```\ncode";
    expect(toggleList(v, 6, 6)).toBeNull();
    expect(fenceSelection(v, 6, 6)).toBeNull();
  });

  it("fences a selection on its own lines with the selection kept selected", () => {
    const e = fenceSelection("say hi now", 4, 6)!;
    expect(e.value).toBe("say \n```\nhi\n```\n now");
    expect(e.value.slice(e.start, e.end)).toBe("hi");
    expect(inFence(e.value, e.start)).toBe(true);
  });

  it("an empty fence leaves the caret inside it", () => {
    const e = fenceSelection("", 0, 0)!;
    expect(e).toEqual({ value: "```\n\n```", start: 4, end: 4 });
  });

  it("Tab indents only at a caret inside a fence", () => {
    expect(indentInFence("```\nx", 5, 5)!.value).toBe("```\nx  ");
    expect(indentInFence("```\nx", 4, 5)).toBeNull();
    expect(indentInFence("plain", 5, 5)).toBeNull();
  });
});

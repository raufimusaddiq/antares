// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Markdown } from "./Markdown";

const md = (text: string) => renderToStaticMarkup(<Markdown text={text} />);
afterEach(cleanup);

describe("Markdown in a real DOM", () => {
  it("keeps a hostile document inert: script, onerror, javascript: URL and raw HTML block", () => {
    const hostile = [
      "<script>alert(1)</script>",
      "",
      '<div onclick="x()"><img src=x onerror=alert(1)></div>',
      "",
      "[click](javascript:alert(1)) [ok](https://example.test) <svg/onload=alert(1)>",
      "",
      "| <img src=x onerror=alert(1)> | b |",
      "|---|---|",
      "| [x](javascript:alert(1)) | `<script>` |",
      "",
      "```<script>alert(1)</script>",
      "<script>alert(2)</script>",
      "```",
    ].join("\n");
    const host = document.createElement("div");
    host.innerHTML = md(hostile);
    expect(host.querySelector("script,img,svg,iframe,div[onclick]")).toBeNull();
    for (const el of Array.from(host.querySelectorAll("*"))) {
      for (const a of Array.from(el.attributes)) {
        expect(a.name.startsWith("on"), `${el.tagName} ${a.name}`).toBe(false);
        if (a.name === "href") expect(a.value).toBe("https://example.test/");
      }
    }
    expect(host.textContent).toContain("<script>alert(1)</script>");
    expect(host.textContent).toContain("onerror=alert(1)");
    expect(host.textContent).toContain("[click](javascript:alert(1))");
  });

  it("copies the code block text to the clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    render(<Markdown text={"```py\nprint('<b>')\n```"} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Copied" })).toBeTruthy());
    expect(writeText).toHaveBeenCalledWith("print('<b>')");
  });

  it("resets Copied when the block text changes while streaming", async () => {
    Object.defineProperty(navigator, "clipboard", { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true });
    const { rerender } = render(<Markdown text={"```py\nprint(1)"} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Copied" })).toBeTruthy());
    rerender(<Markdown text={"```py\nprint(1)\nprint(2)"} />);
    expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
  });

  it("scrollable code and table regions are keyboard reachable and named", () => {
    const host = document.createElement("div");
    host.innerHTML = md("```py\nx = 1\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |");
    for (const el of Array.from(host.querySelectorAll("pre, .tablewrap"))) {
      expect(el.getAttribute("tabindex")).toBe("0");
      expect(el.getAttribute("role")).toBe("region");
      expect(el.getAttribute("aria-label")).toBeTruthy();
    }
  });

  it("mixed Markdown with inline and fenced code renders as text, not markup", () => {
    const host = document.createElement("div");
    host.innerHTML = md("**b** *i* `<b>x</b>`\n\n- one\n- <u>two</u>\n\n```html\n<img src=x onerror=alert(1)>\n```");
    expect(host.querySelector("strong,em,code,li,pre")).not.toBeNull();
    expect(host.querySelector("img,u,b")).toBeNull();
    expect(host.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(host.textContent).toContain("<u>two</u>");
  });
});

// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import RichBlock from "./Block";
import { ReplyContext } from "./reply";
import { check } from "./validate";

afterEach(cleanup);

const withReply = (src: string, add: ((t: string) => void) | null) =>
  render(
    <ReplyContext.Provider value={add}>
      <RichBlock source={src} />
    </ReplyContext.Provider>,
  );

describe("safe actions", () => {
  it("a Button adds its reply to the composer and sends nothing", () => {
    const add = vi.fn();
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    try {
      withReply('root = Stack([Button("Compare", "Compare A and B")])', add);
      fireEvent.click(screen.getByRole("button", { name: "Compare" }));
      expect(add).toHaveBeenCalledWith("Compare A and B");
      expect(fetchSpy).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("with no composer to write into the button is disabled", () => {
    withReply('root = Stack([Button("Go", "x")])', null);
    expect((screen.getByRole("button", { name: "Go" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("Input adds 'label: text' only once there is text", () => {
    const add = vi.fn();
    withReply('root = Stack([Input("City", "where?")])', add);
    const btn = screen.getByRole("button", { name: "Add to message" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "  Jakarta " } });
    fireEvent.click(btn);
    expect(add).toHaveBeenCalledWith("City: Jakarta");
  });

  it("Choice adds the picked option", () => {
    const add = vi.fn();
    withReply('root = Stack([Choice("Size", ["S", "M"])])', add);
    const btn = screen.getByRole("button", { name: "Add to message" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    fireEvent.click(screen.getByLabelText("M"));
    fireEvent.click(btn);
    expect(add).toHaveBeenCalledWith("Size: M");
  });

  it("reply text from the model is plain text in the handler, not markup", () => {
    const add = vi.fn();
    withReply('root = Stack([Button("<b>x</b>", "<img src=x onerror=alert(1)>")])', add);
    expect(document.querySelector("b")).toBeNull();
    fireEvent.click(screen.getByRole("button"));
    expect(add).toHaveBeenCalledWith("<img src=x onerror=alert(1)>");
    expect(document.querySelector("img")).toBeNull();
  });
});

describe("charts", () => {
  it.each(["bar", "line", "pie"])("%s chart loads on demand and shows its numbers as text", async (kind) => {
    const { container } = withReply(`root = Stack([Chart("${kind}", "Sales <b>", ["Q1", "Q2", "Q3"], [3, 5.255, 2])])`, null);
    await waitFor(() => expect(container.querySelector("svg")).not.toBeNull());
    expect(container.querySelector("figure")?.getAttribute("data-ou-chart")).toBe(kind);
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toContain("Sales <b>");
    expect(container.textContent).toContain("Q2");
    expect(container.textContent).toContain("5.26");
  });

  it("mismatched labels and values are refused with the source shown, not drawn", () => {
    const src = 'root = Stack([Chart("bar", "t", ["a", "b"], [1])])';
    expect(check(src)).toEqual({ ok: false, reason: "chart labels and values differ" });
    const { container } = withReply(src, null);
    expect(container.textContent).toContain("could not be displayed");
    expect(container.querySelector("svg")).toBeNull();
  });

  it("an all-zero pie says there is nothing to chart", async () => {
    const { container } = withReply('root = Stack([Chart("pie", "t", ["a"], [0])])', null);
    await waitFor(() => expect(container.textContent).toContain("No data to chart"));
  });
});

describe("unknown components stay refused", () => {
  it("shows source as text and renders nothing from an unregistered component", () => {
    const { container } = withReply('root = Stack([Text("ok"), Video("https://evil.test/v.mp4"), Chart2("bar")])', vi.fn());
    expect(container.textContent).toContain("could not be displayed");
    expect(container.querySelector("video, iframe, a, svg, button")).toBeNull();
  });
});

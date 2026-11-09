// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RowMenu } from "./RowMenu";

afterEach(cleanup);

function mount() {
  const run = { open: vi.fn(), del: vi.fn() };
  render(
    <>
      <button type="button">outside</button>
      <RowMenu label="More actions for X" actions={[{ label: "Open", run: run.open }, { label: "View post", href: "/b/x" }, { label: "Duplicate", run: () => {} }, { label: "Delete", danger: true, run: run.del }]} />
    </>,
  );
  return { run, button: screen.getByRole("button", { name: "More actions for X" }) };
}
const items = () => screen.getAllByRole("menuitem");

describe("the row's ⋯ menu", () => {
  it("is a menu button: closed it says so, open it shows its items and focuses the first", () => {
    const { button } = mount();
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByRole("menu", { name: "More actions for X" })).toBeTruthy();
    expect(items().map((i) => i.textContent)).toEqual(["Open", "View post", "Duplicate", "Delete"]);
    expect(document.activeElement).toBe(items()[0]);
    fireEvent.click(button);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("opens from the arrow keys on the button", () => {
    const { button } = mount();
    button.focus();
    fireEvent.keyDown(button, { key: "ArrowDown" });
    expect(screen.getByRole("menu")).toBeTruthy();
    expect(document.activeElement).toBe(items()[0]);
  });

  it("opens onto the last item from ArrowUp on the button", () => {
    const { button } = mount();
    button.focus();
    fireEvent.keyDown(button, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items()[3]);
  });

  it("moves with the arrows, wraps, and jumps with Home and End", () => {
    const { button } = mount();
    fireEvent.click(button);
    const menu = screen.getByRole("menu");
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items()[1]);
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement).toBe(items()[3]);
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items()[0]); // wraps
    fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items()[3]); // wraps back
    fireEvent.keyDown(menu, { key: "Home" });
    expect(document.activeElement).toBe(items()[0]);
  });

  it("closes on Escape and gives focus back to its button", () => {
    const { button } = mount();
    fireEvent.click(button);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(button);
  });

  it("closes when Tab leaves it, or when something outside is pressed", () => {
    const { button } = mount();
    fireEvent.click(button);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Tab" });
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(button);
    fireEvent.pointerDown(screen.getByRole("button", { name: "outside" }));
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(button);
    fireEvent.pointerDown(screen.getByRole("menu")); // a press inside it does not close it
    expect(screen.getByRole("menu")).toBeTruthy();
  });

  it("runs the chosen item and closes; a link item has its address; a dangerous one is marked", () => {
    const { button, run } = mount();
    fireEvent.click(button);
    expect(screen.getByRole("menuitem", { name: "View post" }).getAttribute("href")).toBe("/b/x");
    expect(screen.getByRole("menuitem", { name: "Delete" }).className).toBe("danger");
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(run.del).toHaveBeenCalledTimes(1);
    expect(run.open).not.toHaveBeenCalled();
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(button); // not left on the page body
  });
});

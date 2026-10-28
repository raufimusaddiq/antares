// @vitest-environment jsdom
// #139 stage 3: a turn shows only what works. Regenerate, edit and rewind have no route in the assistant backend, so
// they are not shown at all; the working alternative (#166) writes the earlier text into the composer.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { TurnControls } from "./Chat";

afterEach(cleanup);

describe("turn controls", () => {
  it("show the working action, and nothing that cannot act", () => {
    const onClick = vi.fn();
    render(<TurnControls reuse={{ label: "Ask again", onClick }} />);
    fireEvent.click(screen.getByRole("button", { name: "Ask again" }));
    expect(onClick).toHaveBeenCalledOnce();
    expect(screen.queryByRole("button", { name: /Regenerate|Rewind|^Edit$/ })).toBeNull();
    expect(document.querySelector("[aria-disabled]")).toBeNull();
  });

  it("render nothing when there is no working action", () => {
    const { container } = render(<TurnControls />);
    expect(container.innerHTML).toBe("");
  });
});

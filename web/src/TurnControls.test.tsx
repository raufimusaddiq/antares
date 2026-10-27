// @vitest-environment jsdom
// The capability reason behind Edit / Regenerate / Rewind must be reachable by a tap: iOS Safari does not focus a tapped
// button, so :hover and :focus alone would hide it from a phone user (#139). jsdom applies no CSS, so this proves the state,
// the ARIA contract and the dismissal; whether the shown reason covers text is measured in a real browser (scripts/browser-check.mjs).
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { TurnControls } from "./Chat";
import { parseInventory } from "./chatApi";

afterEach(cleanup);

const inv = parseInventory({
  capabilities: [
    { id: "regenerate", label: "Regenerate", status: "unsupported", reason: "no retry route" },
    { id: "rewind", label: "Rewind", status: "unsupported", reason: "no truncate route" },
  ],
  toolsets: [], providers: [], skills: [],
});

const mount = () => render(<TurnControls inv={inv} kinds={["regenerate", "rewind"]} seq={3} />);
const shown = (el: HTMLElement) => el.closest(".turn-control")?.getAttribute("data-reason") === "shown";

describe("capability reasons on touch", () => {
  it("keeps the reason in the document and as the description, shown or not", () => {
    mount();
    for (const [name, reason] of [["Regenerate", "no retry route"], ["Rewind", "no truncate route"]]) {
      const btn = screen.getByRole("button", { name });
      const small = document.getElementById(btn.getAttribute("aria-describedby") ?? "");
      expect(small?.textContent).toBe(reason);
      expect(btn.getAttribute("aria-disabled")).toBe("true");
      expect(shown(btn)).toBe(false);
    }
  });

  it("a tap (click, no hover, no focus) shows the reason, a second tap hides it, one at a time", () => {
    mount();
    const regen = screen.getByRole("button", { name: "Regenerate" });
    const rewind = screen.getByRole("button", { name: "Rewind" });
    fireEvent.click(regen);
    expect(shown(regen)).toBe(true);
    fireEvent.click(rewind);
    expect(shown(rewind)).toBe(true);
    expect(shown(regen)).toBe(false);
    fireEvent.click(rewind);
    expect(shown(rewind)).toBe(false);
  });

  it("Escape and a press elsewhere put the reason away; a press inside the controls does not", () => {
    mount();
    const regen = screen.getByRole("button", { name: "Regenerate" });
    fireEvent.click(regen);
    fireEvent.pointerDown(regen);
    expect(shown(regen)).toBe(true);
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(shown(regen)).toBe(false);
    fireEvent.click(regen);
    fireEvent.pointerDown(document.body);
    expect(shown(regen)).toBe(false);
  });

  it("is still not an action: the button stays aria-disabled after a tap", () => {
    mount();
    const regen = screen.getByRole("button", { name: "Regenerate" });
    fireEvent.click(regen);
    expect(regen.getAttribute("aria-disabled")).toBe("true");
    expect(regen.getAttribute("data-control")).toBe("regenerate");
  });
});

describe("a working alternative beside the controls the assistant cannot honour (#166)", () => {
  it("offers the alternative as a real button and names it in the disabled controls' reason, not in Rewind's", () => {
    let used = 0;
    render(<TurnControls inv={inv} kinds={["regenerate", "rewind"]} seq={3} reuse={{ label: "Ask again", onClick: () => used++ }} />);
    const regen = screen.getByRole("button", { name: "Regenerate" });
    expect(document.getElementById(regen.getAttribute("aria-describedby")!)?.textContent).toBe("no retry route Instead, use “Ask again”: it writes a new message for you to send.");
    const rewind = screen.getByRole("button", { name: "Rewind" });
    expect(document.getElementById(rewind.getAttribute("aria-describedby")!)?.textContent).toBe("no truncate route");
    const ask = screen.getByRole("button", { name: "Ask again" });
    expect(ask.getAttribute("aria-disabled")).toBeNull();
    fireEvent.click(ask);
    expect(used).toBe(1);
  });
});

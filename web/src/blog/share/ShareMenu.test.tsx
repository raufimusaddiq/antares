// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ShareMenu } from "./ShareMenu";

const URL_OF = `${window.location.origin}/b/self-hosting-costs`;
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Object.defineProperty(navigator, "share", { value: undefined, configurable: true });
});
const open = () => fireEvent.click(screen.getByRole("button", { name: "Share this post" }));
const items = () => screen.getAllByRole("menuitem").map((e) => e.textContent);

describe("Share on a post", () => {
  it("with a native sheet: the first entry hands it the title and the canonical address; no services are listed", async () => {
    const share = vi.fn(async () => {});
    Object.defineProperty(navigator, "share", { value: share, configurable: true });
    render(<ShareMenu title="Self-hosting" slug="self-hosting-costs" onStory={() => {}} />);
    open();
    expect(items()).toEqual(["Share with your apps", "Copy link", "Story image"]);
    fireEvent.click(screen.getByRole("menuitem", { name: "Share with your apps" }));
    expect(share).toHaveBeenCalledWith({ title: "Self-hosting", url: URL_OF });
  });

  it("a cancelled native sheet is not an error", async () => {
    const share = vi.fn(async () => {
      throw new DOMException("cancelled", "AbortError");
    });
    Object.defineProperty(navigator, "share", { value: share, configurable: true });
    render(<ShareMenu title="T" slug="self-hosting-costs" />);
    open();
    fireEvent.click(screen.getByRole("menuitem", { name: "Share with your apps" }));
    await Promise.resolve();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("without a native sheet: the services open in a new tab with no opener, then Copy link and Story image", () => {
    render(<ShareMenu title="T" slug="self-hosting-costs" onStory={() => {}} />);
    open();
    expect(items()).toEqual(["Copy link", "WhatsApp", "X", "Facebook", "LinkedIn", "Telegram", "Email", "Story image"]);
    for (const name of ["WhatsApp", "X", "Facebook", "LinkedIn", "Telegram", "Email"]) {
      const a = screen.getByRole("menuitem", { name });
      expect(a.getAttribute("target"), name).toBe("_blank");
      expect(a.getAttribute("rel"), name).toBe("noopener noreferrer");
    }
  });

  it("Copy link copies the canonical address and says so, in the menu and in a status", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", Object.assign(Object.create(navigator), { clipboard: { writeText } }));
    render(<ShareMenu title="T" slug="self-hosting-costs" />);
    open();
    fireEvent.click(screen.getByRole("menuitem", { name: "Copy link" }));
    expect(writeText).toHaveBeenCalledWith(URL_OF);
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("Link copied"));
    // The menu stays open and the item reads as done, but its name stays "Copy link": the status line announces the result once.
    expect(screen.getByRole("menuitem", { name: "Copy link" }).textContent).toBe("Link copied");
  });

  it("keeps keyboard focus on the item when Copy link turns into Link copied", async () => {
    vi.stubGlobal("navigator", Object.assign(Object.create(navigator), { clipboard: { writeText: vi.fn(async () => {}) } }));
    render(<ShareMenu title="T" slug="self-hosting-costs" />);
    open();
    const item = screen.getByRole("menuitem", { name: "Copy link" });
    item.focus();
    fireEvent.click(item);
    await waitFor(() => expect(screen.getByRole("menuitem", { name: "Copy link" }).textContent).toBe("Link copied"));
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Copy link" }));
  });

  it("when the Clipboard API refuses, the address is shown to copy by hand", async () => {
    const writeText = vi.fn(async () => {
      throw new Error("denied");
    });
    vi.stubGlobal("navigator", Object.assign(Object.create(navigator), { clipboard: { writeText } }));
    render(<ShareMenu title="T" slug="self-hosting-costs" />);
    open();
    fireEvent.click(screen.getByRole("menuitem", { name: "Copy link" }));
    const field = await screen.findByDisplayValue(URL_OF);
    expect((field as HTMLInputElement).readOnly).toBe(true);
  });

  it("Escape closes the menu and returns focus to the Share button; Story image runs the callback", () => {
    const onStory = vi.fn();
    render(<ShareMenu title="T" slug="self-hosting-costs" onStory={onStory} />);
    open();
    const first = screen.getAllByRole("menuitem")[0];
    fireEvent.keyDown(first, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Share this post" }));
    open();
    fireEvent.click(screen.getByRole("menuitem", { name: "Story image" }));
    expect(onStory).toHaveBeenCalledTimes(1);
  });
});

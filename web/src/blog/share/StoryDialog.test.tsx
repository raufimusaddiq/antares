// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StoryDialog } from "./StoryDialog";
import { renderPostCard, type PostCardData } from "./postCard";

vi.mock("./postCard", () => ({ renderPostCard: vi.fn(async () => new Blob(["png"], { type: "image/png" })) }));
vi.mock("../../exportChat", () => ({ saveBlob: vi.fn() }));
import { saveBlob } from "../../exportChat";

const drawn = vi.mocked(renderPostCard);
const DATA: PostCardData = { title: "Self-hosting", summary: "What it costs.", highlights: ["One"], authorName: "Maya R.", hasCover: false, coverUrl: "", date: new Date(2026, 10, 9), address: "antares.example/b/self-hosting" };
const URL_OF = "https://antares.example/b/self-hosting";
URL.createObjectURL = () => "blob:x";
URL.revokeObjectURL = () => {};

beforeEach(() => {
  drawn.mockClear();
  vi.mocked(saveBlob).mockClear();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Object.defineProperty(navigator, "share", { value: undefined, configurable: true });
  Object.defineProperty(navigator, "canShare", { value: undefined, configurable: true });
});
const dialog = () => render(<StoryDialog data={DATA} url={URL_OF} slug="self-hosting" onClose={() => {}} />);
const clip = (writeText: () => Promise<void>) => vi.stubGlobal("navigator", Object.assign(Object.create(navigator), { clipboard: { writeText } }));

describe("Share as a story", () => {
  it("draws the picture when it opens and describes it for a screen reader", async () => {
    dialog();
    expect(screen.getByText("Making your picture…")).toBeTruthy();
    const img = await screen.findByRole("img");
    expect(img.getAttribute("alt")).toContain("Self-hosting");
    expect(img.getAttribute("alt")).toContain("What it costs.");
    expect(drawn).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/Nothing is uploaded/)).toBeTruthy();
  });

  it("offers Share to an app only when the device can send a picture, and sends the file with the title and address", async () => {
    const share = vi.fn(async () => {});
    Object.defineProperty(navigator, "share", { value: share, configurable: true });
    Object.defineProperty(navigator, "canShare", { value: () => true, configurable: true });
    dialog();
    await screen.findByRole("img");
    fireEvent.click(screen.getByRole("button", { name: "Share to an app" }));
    expect(share).toHaveBeenCalledTimes(1);
    const arg = (share.mock.calls[0] as unknown as [{ files: File[]; title: string; url: string }])[0];
    expect(arg.files[0].name).toBe("antares-self-hosting.png");
    expect(arg.title).toBe("Self-hosting");
    expect(arg.url).toBe(URL_OF);
  });

  it("without a share sheet there is no Share to an app: Save saves the picture, copies the link and says what to do next", async () => {
    const writeText = vi.fn(async () => {});
    clip(writeText);
    dialog();
    await screen.findByRole("img");
    expect(screen.queryByRole("button", { name: "Share to an app" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Save image" }));
    expect(saveBlob).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith(URL_OF);
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/add a link sticker and paste the link/));
  });

  it("a failure is said in plain words, Copy link still works, Try again draws again, and the post is untouched", async () => {
    drawn.mockResolvedValueOnce(null);
    const writeText = vi.fn(async () => {});
    clip(writeText);
    dialog();
    expect((await screen.findByRole("alert")).textContent).toMatch(/Could not make the picture/);
    fireEvent.click(screen.getByRole("button", { name: "Copy link" }));
    expect(writeText).toHaveBeenCalledWith(URL_OF);
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByRole("img");
    expect(drawn).toHaveBeenCalledTimes(2);
  });

  it("when the clipboard refuses, the address is shown to copy by hand", async () => {
    clip(async () => {
      throw new Error("denied");
    });
    dialog();
    await screen.findByRole("img");
    fireEvent.click(screen.getByRole("button", { name: "Copy link" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain(URL_OF));
  });

  it("is a modal dialog labelled for what it does", async () => {
    dialog();
    await screen.findByRole("img");
    const d = document.querySelector("dialog")!;
    expect(d.getAttribute("aria-labelledby")).toBe("story-title");
    expect(document.getElementById("story-title")!.textContent).toBe("Share as a story");
  });
});

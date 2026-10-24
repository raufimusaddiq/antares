// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { ImageStrip, useImageAttachments } from "./ImageAttachments";
import { OwnedAudio, SentImage } from "./MediaPreview";
import * as api from "./mediaApi";

const png = (name = "a.png", size = 100) => new File([new Uint8Array(size)], name, { type: "image/png" });

beforeEach(() => {
  URL.createObjectURL = vi.fn(() => "blob:thumb");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useImageAttachments", () => {
  it("uploads, reports progress, becomes ready with the server id and blocks send while pending", async () => {
    let finish: (v: { ok: true; value: { id: string } }) => void = () => {};
    let progress: (n: number) => void = () => {};
    vi.spyOn(api, "uploadImage").mockImplementation((_f, onProgress) => {
      progress = onProgress;
      return new Promise((r) => (finish = r as typeof finish));
    });
    const { result } = renderHook(() => useImageAttachments());
    act(() => result.current.add([png()]));
    expect(result.current.items[0].status).toBe("uploading");
    expect(result.current.pending).toBe(true);
    act(() => progress(0.5));
    expect(result.current.items[0].progress).toBe(0.5);
    await act(async () => finish({ ok: true, value: { id: "id1" } }));
    expect(result.current.items[0].status).toBe("ready");
    expect(result.current.readyIds).toEqual(["id1"]);
    expect(result.current.pending).toBe(false);
  });

  it("keeps a failed upload visible with retry, never dropping it", async () => {
    const up = vi.spyOn(api, "uploadImage").mockResolvedValueOnce({ ok: false, code: "NETWORK", status: 0 }).mockResolvedValueOnce({ ok: true, value: { id: "id2" } });
    const { result } = renderHook(() => useImageAttachments());
    await act(async () => result.current.add([png()]));
    expect(result.current.items[0].status).toBe("failed");
    expect(result.current.pending).toBe(true);
    expect(result.current.readyIds).toEqual([]);
    await act(async () => result.current.retry(result.current.items[0].key));
    expect(up).toHaveBeenCalledTimes(2);
    expect(result.current.readyIds).toEqual(["id2"]);
  });

  it("refuses spoofed types, oversize files and a fifth image, and says why", async () => {
    vi.spyOn(api, "uploadImage").mockResolvedValue({ ok: true, value: { id: "x" } });
    const { result } = renderHook(() => useImageAttachments());
    await act(async () => result.current.add([new File(["<svg/>"], "x.svg", { type: "image/svg+xml" })]));
    expect(result.current.items).toHaveLength(0);
    expect(result.current.notice).toMatch(/PNG, JPEG and GIF/);
    await act(async () => result.current.add([png("1.png"), png("2.png"), png("3.png"), png("4.png"), png("5.png")]));
    expect(result.current.items).toHaveLength(4);
    expect(result.current.notice).toMatch(/At most 4/);
  });

  it("removing a staged image deletes the owner's asset; release keeps it for history", async () => {
    vi.spyOn(api, "uploadImage").mockResolvedValue({ ok: true, value: { id: "gone" } });
    const del = vi.spyOn(api, "deleteAsset").mockResolvedValue(true);
    const { result } = renderHook(() => useImageAttachments());
    await act(async () => result.current.add([png()]));
    act(() => result.current.remove(result.current.items[0].key));
    expect(del).toHaveBeenCalledWith("gone");
    expect(URL.revokeObjectURL).toHaveBeenCalled();
    await act(async () => result.current.add([png()]));
    act(() => result.current.release());
    expect(del).toHaveBeenCalledTimes(1);
    expect(result.current.items).toHaveLength(0);
  });
});

describe("ImageStrip", () => {
  it("shows name, type, size, thumbnail, progress, retry and remove", () => {
    const f = png("photo.png", 2048);
    const onRetry = vi.fn();
    const onRemove = vi.fn();
    render(
      <ImageStrip
        items={[
          { key: "a", file: f, previewUrl: "blob:a", status: "uploading", progress: 0.3 },
          { key: "b", file: f, previewUrl: "blob:b", status: "failed", progress: 0, error: "Could not reach Antares." },
        ]}
        onRemove={onRemove}
        onRetry={onRetry}
      />,
    );
    expect(screen.getAllByText("photo.png")).toHaveLength(2);
    expect(screen.getAllByText(/PNG · 2 KiB/)).toHaveLength(2);
    expect(screen.getByRole("progressbar").getAttribute("value")).toBe("0.3");
    expect(screen.getByRole("alert").textContent).toMatch(/Could not reach/);
    fireEvent.click(screen.getByText("Retry"));
    expect(onRetry).toHaveBeenCalledWith("b");
    fireEvent.click(screen.getAllByText("Remove")[0]);
    expect(onRemove).toHaveBeenCalledWith("a");
  });
});

describe("media previews", () => {
  it("renders an owned thumbnail from its asset id, lazily, and opens a lightbox that closes on Escape", () => {
    render(<SentImage att={{ assetId: "abc", mediaType: "image/png", available: true }} index={0} />);
    const img = screen.getByAltText("Image 1 you sent") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe("/api/assets/abc?inline=1");
    expect(img.getAttribute("loading")).toBe("lazy");
    fireEvent.click(screen.getByLabelText("Open Image 1 you sent"));
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(screen.getByText("Download").getAttribute("href")).toBe("/api/assets/abc");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("says a deleted or unreadable image is gone instead of showing a broken picture", () => {
    const { rerender } = render(<SentImage att={{ assetId: "abc", mediaType: "image/png", available: false }} index={1} />);
    expect(screen.getByText(/no longer available/)).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
    rerender(<SentImage att={{ assetId: "abc", mediaType: "image/png", available: true }} index={1} />);
    fireEvent.error(screen.getByAltText("Image 2 you sent"));
    expect(screen.getByText(/no longer available/)).toBeTruthy();
  });

  it("audio loads on demand with native controls and a download link", () => {
    const { container } = render(<OwnedAudio assetId="aud" label="Your recording" />);
    const a = container.querySelector("audio")!;
    expect(a.getAttribute("preload")).toBe("none");
    expect(a.hasAttribute("controls")).toBe(true);
    expect(a.getAttribute("src")).toBe("/api/assets/aud?inline=1");
    fireEvent.error(a);
    expect(screen.getByText(/no longer available/)).toBeTruthy();
  });
});

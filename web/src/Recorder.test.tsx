// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MAX_SECONDS, Recorder } from "./Recorder";

class FakeRecorder {
  static last: FakeRecorder;
  static isTypeSupported = () => true;
  state = "inactive";
  mimeType = "audio/webm";
  stream = { getTracks: () => [{ stop: stopTrack }] };
  ondataavailable: ((e: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  constructor() {
    FakeRecorder.last = this;
  }
  start() {
    this.state = "recording";
  }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: new Blob(["x"]) });
    this.onstop?.();
  }
}
const stopTrack = vi.fn();
const fetchSpy = vi.fn();

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("MediaRecorder", FakeRecorder);
  vi.stubGlobal("fetch", fetchSpy);
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] }) } });
  URL.createObjectURL = vi.fn(() => "blob:take");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("Recorder", () => {
  it("records, previews locally and discards without any network call", async () => {
    render(<Recorder />);
    await act(async () => fireEvent.click(screen.getByText("Record")));
    act(() => void vi.advanceTimersByTime(3000));
    expect(screen.getByRole("timer").textContent).toContain("0:03");
    fireEvent.click(screen.getByText("Stop"));
    expect(screen.getByLabelText("Your recording").getAttribute("src")).toBe("blob:take");
    expect(stopTrack).toHaveBeenCalled();
    fireEvent.click(screen.getByText("Discard"));
    expect(screen.queryByLabelText("Your recording")).toBeNull();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:take");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("stops by itself at the duration ceiling", async () => {
    render(<Recorder />);
    await act(async () => fireEvent.click(screen.getByText("Record")));
    act(() => void vi.advanceTimersByTime(MAX_SECONDS * 1000));
    expect(screen.getByLabelText("Your recording")).toBeTruthy();
  });

  it("says why when the microphone is refused", async () => {
    (navigator.mediaDevices.getUserMedia as ReturnType<typeof vi.fn>).mockRejectedValue(new DOMException("no", "NotAllowedError"));
    render(<Recorder />);
    await act(async () => fireEvent.click(screen.getByText("Record")));
    expect(screen.getByRole("alert").textContent).toContain("refused");
  });

  it("states that speech-to-text is unavailable rather than offering it", () => {
    render(<Recorder />);
    expect(screen.getByText(/Speech-to-text is unavailable/)).toBeTruthy();
    expect(screen.queryByText(/transcribe/i)).toBeNull();
  });
});

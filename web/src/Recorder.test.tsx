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

  it("releases the microphone when the recorder fails to start", async () => {
    const start = vi.spyOn(FakeRecorder.prototype, "start").mockImplementation(() => {
      throw new DOMException("bad state", "InvalidStateError");
    });
    render(<Recorder />);
    await act(async () => fireEvent.click(screen.getByText("Record")));
    expect(stopTrack).toHaveBeenCalled();
    expect(screen.getByRole("alert")).toBeTruthy();
    start.mockRestore();
  });

  it("stops a stream that resolves after unmount and starts no recorder", async () => {
    let grant!: (s: unknown) => void;
    const lateTrack = vi.fn();
    (navigator.mediaDevices.getUserMedia as ReturnType<typeof vi.fn>).mockReturnValue(new Promise((r) => (grant = r)));
    const made = vi.fn();
    class Spy extends FakeRecorder {
      constructor() {
        super();
        made();
      }
    }
    vi.stubGlobal("MediaRecorder", Spy);
    const { unmount } = render(<Recorder />);
    fireEvent.click(screen.getByText("Record"));
    unmount();
    await act(async () => grant({ getTracks: () => [{ stop: lateTrack }] }));
    expect(lateTrack).toHaveBeenCalledTimes(1);
    expect(made).not.toHaveBeenCalled();
  });

  it("allows one getUserMedia call and one recorder while permission is pending", async () => {
    let grant!: (s: unknown) => void;
    const gum = navigator.mediaDevices.getUserMedia as ReturnType<typeof vi.fn>;
    gum.mockReturnValue(new Promise((r) => (grant = r)));
    const made = vi.fn();
    class Spy extends FakeRecorder {
      constructor() {
        super();
        made();
      }
    }
    vi.stubGlobal("MediaRecorder", Spy);
    render(<Recorder />);
    const btn = screen.getByText("Record");
    fireEvent.click(btn);
    fireEvent.click(btn);
    fireEvent.click(btn);
    expect((btn as HTMLButtonElement).disabled).toBe(true);
    await act(async () => grant({ getTracks: () => [{ stop: stopTrack }] }));
    expect(gum).toHaveBeenCalledTimes(1);
    expect(made).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("timer")).toBeTruthy();
  });

  it("states that recordings are not transcribed rather than offering it", () => {
    render(<Recorder />);
    expect(screen.getByText(/Recordings are not transcribed/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /transcri/i })).toBeNull();
  });
});

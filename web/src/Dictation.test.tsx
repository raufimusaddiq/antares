// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Dictation } from "./Dictation";

type Handler = ((e: unknown) => void) | null;
let last: FakeRecognition | null = null;
class FakeRecognition {
  lang = "";
  continuous = false;
  interimResults = true;
  onresult: Handler = null;
  onerror: Handler = null;
  onend: (() => void) | null = null;
  started = false;
  constructor() {
    last = this;
  }
  start() {
    this.started = true;
  }
  stop() {
    this.onend?.();
  }
  abort() {
    this.onend?.();
  }
  say(...phrases: { text: string; final: boolean }[]) {
    const results = phrases.map((p) => ({ isFinal: p.final, 0: { transcript: p.text } }));
    this.onresult?.({ resultIndex: 0, results });
  }
}

beforeEach(() => {
  localStorage.clear();
  last = null;
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// #167: dictation is offered only when the browser can do it, explains where the audio goes before first use, puts
// finished text into the draft for editing and never sends anything.
describe("Dictation", () => {
  it("says it is unavailable when the browser has no speech recognition", () => {
    render(<Dictation onText={() => {}} />);
    expect(screen.queryByRole("button", { name: "Dictate" })).toBeNull();
    expect(screen.getByText(/Dictation is not available in this browser/)).toBeTruthy();
  });

  it("asks once with a disclosure, then delivers only final text", () => {
    vi.stubGlobal("webkitSpeechRecognition", FakeRecognition);
    const got: string[] = [];
    render(<Dictation onText={(t) => got.push(t)} />);
    fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
    expect(screen.getByRole("dialog").textContent).toMatch(/Antares never receives the audio/);
    expect(last).toBeNull(); // nothing listens before the user agrees
    fireEvent.click(screen.getByRole("button", { name: "Use dictation" }));
    expect(last?.started).toBe(true);
    expect(last?.interimResults).toBe(false);
    act(() => last!.say({ text: " half a ", final: false }, { text: " hello there ", final: true }, { text: "and more", final: true }));
    expect(got).toEqual(["hello there and more"]);
    fireEvent.click(screen.getByRole("button", { name: "Stop dictation" }));
    // Agreed once: the next start does not ask again.
    fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("declining starts nothing; a refusal is explained", () => {
    vi.stubGlobal("SpeechRecognition", FakeRecognition);
    render(<Dictation onText={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
    fireEvent.click(screen.getByRole("button", { name: "Not now" }));
    expect(last).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
    fireEvent.click(screen.getByRole("button", { name: "Use dictation" }));
    act(() => last!.onerror?.({ error: "not-allowed" }));
    expect(screen.getByRole("alert").textContent).toMatch(/access was refused/);
  });
});

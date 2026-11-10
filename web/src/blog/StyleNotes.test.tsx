// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PublishFlow } from "./PublishFlow";
import { parseCheck } from "./blogApi";
import type { Check, DocNode, Post } from "./types";

vi.setConfig({ testTimeout: 20000 });
afterEach(cleanup);

const doc: DocNode = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "My own words, unchanged." }] }] };
const note = (kind: Check["notes"][number]["kind"], text: string, afterNode = 0) => ({ kind, text, afterNode });
const check = (notes: Check["notes"]): Check => ({ id: "c1", state: "ready", reason: "", notes, notices: [], blocks: [] });
const post = (c: Check): Post => ({ id: "p1", title: "My post", summary: "", slug: "my-post", coverUrl: "", status: "ready", latestVersion: 2, publishedVersion: 0, doc, check: c, updatedAt: "" }) as Post;
const mount = (c: Check) =>
  render(<PublishFlow post={post(c)} caps={{ writing: true, check: true, webSearch: true }} flush={async () => true} onPost={() => {}} onBackToEditing={() => {}} onMyPosts={() => {}} />);

describe("style notes in the check result", () => {
  it("shows a style note with its label and place", () => {
    mount(check([note("style", "Hype wording in the opening.", 2)]));
    expect(screen.getByText("Style")).toBeTruthy();
    expect(screen.getByText("after part 2")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Notes from the assistant (1)" })).toBeTruthy();
  });

  it("dismisses a note, moves focus to the next one, and says so", () => {
    mount(check([note("style", "First note."), note("style", "Second note."), note("clarity", "Third note.")]));
    const buttons = screen.getAllByRole("button", { name: "Dismiss this note" });
    expect(buttons).toHaveLength(3);
    fireEvent.click(buttons[0]);
    expect(screen.queryByText("First note.")).toBeNull();
    expect(screen.getByRole("heading", { name: "Notes from the assistant (2)" })).toBeTruthy();
    expect(screen.getByText("1 note dismissed.")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getAllByRole("button", { name: "Dismiss this note" })[0]);
  });

  it("moves focus to the heading when the last note goes, and never blocks publishing", () => {
    mount(check([note("style", "Only note.")]));
    const publish = screen.getAllByRole("button").filter((b) => /publish/i.test(b.textContent ?? ""));
    expect(publish.length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "Dismiss this note" }));
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Notes from the assistant (0)" }));
    for (const b of publish) expect((b as HTMLButtonElement).disabled).toBe(false);
  });

  it("does not bring a dismissed note back on re-render", () => {
    const c = check([note("style", "Gone soon."), note("fact", "Stays.")]);
    const { rerender } = mount(c);
    fireEvent.click(screen.getAllByRole("button", { name: "Dismiss this note" })[0]);
    rerender(<PublishFlow post={post(c)} caps={{ writing: true, check: true, webSearch: true }} flush={async () => true} onPost={() => {}} onBackToEditing={() => {}} onMyPosts={() => {}} />);
    expect(screen.queryByText("Gone soon.")).toBeNull();
    expect(screen.getByText("Stays.")).toBeTruthy();
  });
});

describe("parseCheck", () => {
  const raw = (kind: string) => ({ id: "c", state: "ready", reason: "", notes: [{ kind, text: "t", afterNode: 0 }], notices: [], blocks: [] });
  it("accepts style and turns an unknown kind into clarity", () => {
    expect(parseCheck(raw("style"))!.notes[0].kind).toBe("style");
    expect(parseCheck(raw("banner"))!.notes[0].kind).toBe("clarity");
  });
});

// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PostSettings } from "./PostSettings";
import { renderPostCard } from "./share/postCard";
import type { Post } from "./types";

vi.mock("./share/postCard", () => ({ renderPostCard: vi.fn(async () => new Blob(["png"], { type: "image/png" })) }));
URL.createObjectURL = () => "blob:preview";
URL.revokeObjectURL = () => {};
const drawn = vi.mocked(renderPostCard);

const doc = { type: "doc", content: [{ type: "heading", attrs: { level: 2 }, content: [{ type: "text", text: "The three options" }] }, { type: "paragraph", content: [{ type: "text", text: "Body." }] }] };
const POST: Post = { id: "p1", title: "Self-hosting", summary: "What it costs.", slug: "self-hosting", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc, check: null, updatedAt: "2026-11-09T01:00:00Z" };

beforeEach(() => drawn.mockClear());
afterEach(cleanup);
const open = (post: Post = POST) => render(<PostSettings post={post} onSaved={() => {}} onClose={() => {}} authorName="Maya R." />);

// Spec 002 FR-015: the author sees how the post will be shared, from the draft, and cannot send it from here.
describe("Share preview in Post settings", () => {
  it("shows the story picture made from the draft, labelled as a preview only", async () => {
    open();
    const img = await screen.findByRole("img", { name: /Preview of the story picture for Self-hosting/ });
    expect(img.getAttribute("src")).toBe("blob:preview");
    expect(screen.getByText(/Preview only/)).toBeTruthy();
    const arg = drawn.mock.calls[0][0];
    expect(arg).toMatchObject({ title: "Self-hosting", summary: "What it costs.", highlights: ["The three options"], authorName: "Maya R.", address: `${window.location.host}/b/self-hosting` });
  });

  it("offers no way to send, save or copy it", async () => {
    open();
    await screen.findByRole("img", { name: /Preview of the story picture/ });
    for (const name of [/share/i, /save image/i, /copy/i, /send/i]) expect(screen.queryByRole("button", { name })).toBeNull();
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy(); // the settings' own Save is the only one
  });

  it("is drawn again from the summary as the author types", async () => {
    open();
    await screen.findByRole("img", { name: /Preview of the story picture/ });
    fireEvent.change(screen.getByRole("textbox", { name: "Summary" }), { target: { value: "A new summary." } });
    await waitFor(() => expect(drawn.mock.calls.at(-1)![0].summary).toBe("A new summary."));
  });

  it("says what is missing instead of drawing a post that has no title", () => {
    open({ ...POST, title: "  " });
    expect(screen.getByText(/Give the post a title/)).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
    expect(drawn).not.toHaveBeenCalled();
  });
});

// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ShareImageDialog } from "./ShareChat";
import { renderShareImage } from "./shareImage";

vi.mock("./shareImage", () => ({ renderShareImage: vi.fn(async () => new Blob(["png"], { type: "image/png" })) }));
URL.createObjectURL = () => "blob:x";
URL.revokeObjectURL = () => {};
const drawn = vi.mocked(renderShareImage);

// #261: the picture opens on a summary of the answer; the whole answer is one tap away, and is shown when no summary
// can be written.
describe("Share as image", () => {
  it("draws the summary, and the whole answer on request", async () => {
    const summarize = vi.fn(async () => ({ ok: true as const, value: "# Short\n- one point" }));
    render(<ShareImageDialog question="q?" answer="the long answer" summarize={summarize} onClose={() => {}} />);
    await waitFor(() => expect(drawn).toHaveBeenLastCalledWith("q?", "the long answer", expect.any(Date), "# Short\n- one point"));
    expect(screen.getByRole("button", { name: "Summary" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Full answer" }));
    await waitFor(() => expect(drawn).toHaveBeenLastCalledWith("q?", "the long answer", expect.any(Date), undefined));
    fireEvent.click(screen.getByRole("button", { name: "Summary" }));
    await waitFor(() => expect(drawn).toHaveBeenLastCalledWith("q?", "the long answer", expect.any(Date), "# Short\n- one point"));
    expect(summarize).toHaveBeenCalledTimes(1);
  });
  it("shows the whole answer, and says so, when no summary can be written", async () => {
    const summarize = vi.fn(async () => ({ ok: false as const, code: "SUMMARY_UNAVAILABLE", status: 503 }));
    render(<ShareImageDialog question="q?" answer="the answer" summarize={summarize} onClose={() => {}} />);
    await waitFor(() => expect(drawn).toHaveBeenLastCalledWith("q?", "the answer", expect.any(Date), undefined));
    expect(screen.getByText(/No summary could be written/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Full answer" }).getAttribute("aria-pressed")).toBe("true");
  });
});

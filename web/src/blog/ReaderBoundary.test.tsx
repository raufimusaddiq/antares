// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

// A block that passes the gate but throws while it draws must show its plain text, and must not take the rest of the post with it.
vi.mock("@openuidev/react-lang", async (importOriginal) => {
  const real = await importOriginal<typeof import("@openuidev/react-lang")>();
  const Renderer = {
    ...real.Renderer,
    Root: ({ children }: { children: unknown }) => children,
    Content: () => {
      throw new Error("a component failed while drawing");
    },
  };
  return { ...real, Renderer };
});

import ReaderBlock from "./ReaderBlock";

afterEach(cleanup);

describe("a reader block that throws while drawing", () => {
  it("falls back to its plain text, and a block beside it still draws", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <>
        <ReaderBlock markup={'root = Card([t])\nt = TextContent("x")'} fallback="The plain version." />
        <p>The rest of the post.</p>
      </>,
    );
    await waitFor(() => expect(screen.getByText("The plain version.")).toBeTruthy());
    expect(screen.getByText("The rest of the post.")).toBeTruthy();
  });
});

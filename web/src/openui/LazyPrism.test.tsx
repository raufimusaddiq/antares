// @vitest-environment jsdom
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { LazyPrism } from "./LazyPrism";

describe("LazyPrism", () => {
  it("shows the code at once, then highlights it", async () => {
    const { container } = render(<LazyPrism language="javascript" PreTag="div" className="hl">{"const a = 1;"}</LazyPrism>);
    expect(screen.getByText("const a = 1;")).toBeTruthy();
    await waitFor(() => expect(container.querySelectorAll(".token").length).toBeGreaterThan(0), { timeout: 5000 });
    expect(container.textContent).toBe("const a = 1;");
  });
});

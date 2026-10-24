// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MessageBody } from "./MessageBody";

// The on-demand chunk cannot be fetched (offline, never loaded once).
vi.mock("./openui/Block", () => {
  throw new Error("Failed to fetch dynamically imported module");
});

afterEach(cleanup);

describe("MessageBody when the OpenUI chunk fails to load", () => {
  it("shows the block source and keeps the rest of the message instead of unmounting the app", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    render(<MessageBody text={"before\n```openui\nroot = Stack([Text(\"x\")])\n```\nafter"} streaming={false} />);
    expect(await screen.findByText(/could not load/)).toBeTruthy();
    expect(screen.getByText('root = Stack([Text("x")])')).toBeTruthy();
    expect(screen.getByText("before")).toBeTruthy();
    expect(screen.getByText("after")).toBeTruthy();
  });
});

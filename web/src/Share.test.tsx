// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ShareChat } from "./ShareChat";
import { SharedChat } from "./SharedChat";
import { answerText, plainText, wrap } from "./shareImage";

const json = (status: number, body?: unknown) => new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const TOKEN = "A".repeat(40) + "b-_";

afterEach(() => vi.unstubAllGlobals());

// #250: a chat shared by link, and an answer drawn as a picture.
describe("Share", () => {
  it("makes a link, shows its address once, and stops it", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init?: RequestInit) => {
        calls.push(`${init?.method ?? "GET"} ${path}`);
        if (init?.method === "POST") return json(201, { token: TOKEN, createdAt: "2026-11-01T10:00:00Z", turns: 4 });
        if (init?.method === "DELETE") return new Response(null, { status: 204 });
        return json(200, { shared: false });
      }),
    );
    render(<ShareChat chatId="c1" />);
    expect(calls).toEqual([]); // nothing is read until the panel opens
    fireEvent.click(screen.getByRole("button", { name: "Share link" }));
    fireEvent.click(await screen.findByRole("button", { name: "Create link" }));
    expect(((await screen.findByLabelText("Share link")) as HTMLInputElement).value).toBe(`${window.location.origin}/s/${TOKEN}`);
    fireEvent.click(screen.getByRole("button", { name: "Stop sharing" }));
    expect(await screen.findByText(/Sharing stopped/)).toBeTruthy();
    expect(calls).toEqual(["GET /api/chats/c1/share", "POST /api/chats/c1/share", "DELETE /api/chats/c1/share"]);
  });

  it("shows a shared chat without a session, and says so when the link is dead", async () => {
    let seen = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        seen++;
        return seen === 1 ? json(200, { title: "Trip plan", sharedAt: "2026-11-01T10:00:00Z", turns: [{ role: "user", text: "where to?" }, { role: "assistant", text: "**Bali**" }, { role: "assistant", text: 'root = Card([h])\nh = CardHeader("Day one")' }] }) : json(404, {});
      }),
    );
    const { unmount } = render(<SharedChat token={TOKEN} />);
    expect(await screen.findByRole("heading", { name: "Trip plan" })).toBeTruthy();
    expect(screen.getByText("Bali").tagName).toBe("STRONG");
    expect(await screen.findByText("Day one", {}, { timeout: 5000 })).toBeTruthy(); // an OpenUI answer is drawn, not shown as code
    expect(screen.queryByText(/root = Card/)).toBeNull();
    unmount();
    render(<SharedChat token={TOKEN} />);
    await waitFor(() => expect(screen.getByRole("heading", { name: "This link does not open" })).toBeTruthy());
  });

  it("draws an answer as plain words that fit the card", () => {
    expect(plainText("## Plan\n- **Day 1**: see [the map](https://x.test)\nMEDIA:image-abc")).toBe("Plan\n• Day 1: see the map (https://x.test)");
    const measure = (s: string) => s.length * 10;
    expect(wrap("one two three four", 90, measure, 5)).toEqual({ lines: ["one two", "three", "four"], cut: false });
    expect(wrap("abcdefghijkl", 50, measure, 5).lines).toEqual(["abcde", "fghij", "kl"]);
    const cut = wrap("a b c d e f", 30, measure, 2);
    expect(cut.cut).toBe(true);
    expect(cut.lines[1].endsWith("…")).toBe(true);
  });

  it("draws an OpenUI answer as its words, not its program", async () => {
    const program = [
      "root = Card([header, text, facts, followups])",
      'header = CardHeader("What I know")',
      'text = MarkDownRenderer("Here is a **summary**.")',
      'facts = EntityList([row1, row2], "default")',
      'row1 = {left: "Name", right: "Raufi"}',
      'row2 = {left: "Zone", right: "WIB"}',
      "followups = FollowUpBlock([f1])",
      'f1 = FollowUpItem("Update my profile")',
    ].join("\n");
    expect(await answerText(program)).toBe("What I know\nHere is a **summary**.\nName: Raufi\nZone: WIB");
    expect(await answerText("plain **text**")).toBe("plain **text**");
  });
});

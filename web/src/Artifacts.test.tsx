// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Artifacts } from "./Artifacts";
import { artifactUrl, findArtifacts } from "./artifacts";
import { fileName, toMarkdown } from "./exportChat";

const TEXT = "Look:\n```html\n<script>top.location='https://evil.example'</script>\n```\n```py\nx=1\n```\n```svg\n<svg onload='alert(1)'/>\n```\n```html\nstill streaming";

// The same table internal/chat/artifacts_test.go runs against the Go parser.
const shared = JSON.parse(readFileSync(join(__dirname, "../../internal/chat/testdata/artifact_cases.json"), "utf8")) as { name: string; text?: string; repeat?: { text: string; times: number }[]; want: string[] }[];

describe("artifact discovery matches the server", () => {
  it.each(shared.map((c) => [c.name, c] as const))("shared table: %s", (_n, c) => {
    const text = (c.text ?? "") + (c.repeat ?? []).map((r) => r.text.repeat(r.times)).join(""); // same assembly as the Go test
    expect(findArtifacts(text).map((a) => a.kind)).toEqual(c.want);
  });
  it("counts only closed html/svg fences, in order", () => {
    expect(findArtifacts(TEXT)).toEqual([
      { kind: "html", index: 0 },
      { kind: "svg", index: 1 },
    ]);
    expect(findArtifacts("````svg\n<svg/>\n```\n````")).toEqual([{ kind: "svg", index: 0 }]);
    expect(findArtifacts("no fences")).toEqual([]);
  });
  it("names an artifact by chat, turn and index only", () => {
    expect(artifactUrl("c1", 3, "h/1", 1, true)).toBe("/api/chats/c1/messages/3/artifacts/1/preview?ref=h%2F1");
  });
});

afterEach(cleanup);

describe("the preview frame", () => {
  it("is a token-less sandbox pointed at the server, never inline markup", () => {
    const { container } = render(<Artifacts chatId="c1" seq={1} turnRef="r1" text={TEXT} />);
    expect(container.querySelector("iframe")).toBeNull(); // nothing loads until asked
    fireEvent.click(screen.getAllByRole("button", { name: "Preview" })[0]);
    const f = container.querySelector("iframe")!;
    // sandbox="" : no scripts, no same-origin, no forms, popups, top navigation, downloads.
    expect(f.getAttribute("sandbox")).toBe("");
    expect(f.hasAttribute("srcdoc")).toBe(false);
    expect(f.getAttribute("src")).toBe("/api/chats/c1/messages/1/artifacts/0/preview?ref=r1");
    expect(f.getAttribute("referrerpolicy")).toBe("no-referrer");
    // The model's markup never enters this document.
    expect(container.innerHTML).not.toContain("evil.example");
    expect(container.querySelector("script")).toBeNull();
  });
  it("offers a named download of the owner's artifact", () => {
    render(<Artifacts chatId="c1" seq={1} turnRef="r1" text={TEXT} />);
    const links = screen.getAllByRole("link", { name: "Download" });
    expect(links.map((l) => l.getAttribute("href"))).toEqual(["/api/chats/c1/messages/1/artifacts/0?ref=r1", "/api/chats/c1/messages/1/artifacts/1?ref=r1"]);
    expect(links[1].getAttribute("download")).toBe("antares-turn1-artifact2.svg");
  });
  it("renders nothing for a turn without artifacts", () => {
    const { container } = render(<Artifacts chatId="c1" seq={1} turnRef="r1" text={"plain"} />);
    expect(container.innerHTML).toBe("");
  });
});

describe("markdown export", () => {
  it("keeps code, tables and image links verbatim and in order", () => {
    const md = toMarkdown("My chat", [
      { seq: 0, role: "user", text: "show me" },
      { seq: 1, role: "assistant", text: "```go\nfmt.Println(1)\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n![chart](https://x.example/c.png)" },
    ], new Date("2026-10-23T10:00:00Z"));
    expect(md).toBe("# My chat\n\nExported 2026-10-23 from Antares.\n\n## You\n\nshow me\n\n## Assistant\n\n```go\nfmt.Println(1)\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n![chart](https://x.example/c.png)\n");
  });
  it("names files safely", () => {
    expect(fileName("../Q3 plan: v2!", "md")).toBe("q3-plan-v2.md");
    expect(fileName("", "md")).toBe("antares-chat.md");
  });
});

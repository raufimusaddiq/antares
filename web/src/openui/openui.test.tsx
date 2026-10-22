import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import RichBlock from "./Block";
import { library, ALLOWED_COMPONENTS } from "./library";
import { MAX_BLOCK_CHARS, splitSegments } from "./segments";
import { check } from "./validate";
import { MessageBody } from "../MessageBody";

const render = (src: string) => renderToStaticMarkup(<RichBlock source={src} />);

const GOOD = 'root = Stack([Heading("Compare"), Table(["Option","Cost"], [["A","$1"],["B","$2"]]), Card("Notes", [Text("hello"), Badge("new")])])';

describe("segments", () => {
  it("plain text is a single Markdown segment", () => {
    expect(splitSegments("hello **there**")).toEqual([{ kind: "md", text: "hello **there**" }]);
  });

  it("finds openui fences and keeps the Markdown around them", () => {
    const s = splitSegments("before\n\n```openui\nroot = Stack([Text(\"x\")])\n```\n\nafter");
    expect(s.map((x) => x.kind)).toEqual(["md", "openui", "md"]);
    expect(s[1]).toMatchObject({ kind: "openui", closed: true, source: 'root = Stack([Text("x")])' });
  });

  it("marks an unfinished block as open, for the streaming placeholder", () => {
    const s = splitSegments("intro\n```openui-lang\nroot = Stack([Text(\"x\"");
    expect(s[1]).toMatchObject({ kind: "openui", closed: false });
  });

  it("an openui fence inside another code fence is just code", () => {
    const s = splitSegments("````md\n```openui\nroot = Stack([])\n```\n````");
    expect(s.every((x) => x.kind === "md")).toBe(true);
  });

  it("over-long or too many blocks stay Markdown", () => {
    const big = "```openui\n" + "x".repeat(MAX_BLOCK_CHARS + 1) + "\n```";
    expect(splitSegments(big).every((x) => x.kind === "md")).toBe(true);
    const many = Array.from({ length: 12 }, () => "```openui\nroot = Stack([])\n```").join("\n");
    expect(splitSegments(many).filter((x) => x.kind === "openui")).toHaveLength(8);
  });
});

describe("the validator is the allowlist", () => {
  it("accepts a valid block", () => {
    expect(check(GOOD)).toEqual({ ok: true });
  });

  const rejected: Record<string, string> = {
    "unknown component": 'root = Stack([Evil("x")])',
    "button with an action": 'root = Stack([Button("go", @OpenUrl("https://evil.test"))])',
    "iframe": 'root = Stack([IFrame("https://evil.test")])',
    "image": 'root = Stack([Image("https://evil.test/x.png")])',
    "link": 'root = Stack([Link("x", "javascript:alert(1)")])',
    "admin query": 'q = Query("admin_users", {}, {rows: []})\nroot = Stack([Text("x")])',
    "mutation": 'm = Mutation("delete_everything", {})\nroot = Stack([Text("x")])',
    "fetch in a query": 'q = Query("fetch", {url: "/api/admin/users"}, {})\nroot = Stack([Text("x")])',
    "state variable": '$x = "a"\nroot = Stack([Text($x)])',
    "inline query": 'root = Stack([Text("a"), Query("x")])',
    "stack inside a card": 'root = Stack([Card("t", [Stack([])])])',
    "card inside a card": 'root = Stack([Card("t", [Card("u", [])])])',
    "root is not a stack": 'root = Text("x")',
    "unfinished": 'root = Stack([Text("a")',
    "missing required prop": 'root = Stack([Metric("only a label")])',
    "dangling reference": 'root = Stack([missing])',
    "orphan statement": 'root = Stack([Text("a")])\nextra = Text("unused")',
    "empty": "",
    "no root": 'a = Text("x")',
  };
  for (const [name, src] of Object.entries(rejected)) {
    it(`rejects ${name}`, () => {
      const v = check(src);
      expect(v.ok, JSON.stringify(v)).toBe(false);
    });
  }

  it("bounds depth and size", () => {
    let deep = 'Text("x")';
    for (let i = 0; i < 8; i++) deep = `Card("c", [${deep}])`;
    expect(check(`root = Stack([${deep}])`).ok).toBe(false);
    const wide = `root = Stack([${Array.from({ length: 41 }, () => 'Text("x")').join(",")}])`;
    expect(check(wide).ok).toBe(false);
    expect(check(`root = Stack([Table(["a"], [${Array.from({ length: 101 }, () => '["x"]').join(",")}])])`).ok).toBe(false);
    expect(check(`root = Stack([Text("${"x".repeat(5000)}")])`).ok).toBe(false);
  });

  it("the library defines nothing that can act", () => {
    expect(ALLOWED_COMPONENTS).toEqual(["Stack", "Card", "Text", "Heading", "Badge", "Metric", "BulletList", "Table", "Divider"]);
    const names = Object.keys(library.toJSONSchema().$defs ?? {});
    expect(names.sort()).toEqual([...ALLOWED_COMPONENTS].sort());
  });
});

describe("rendering", () => {
  it("renders a valid block through the allowlist", () => {
    const html = render(GOOD);
    expect(html).toContain("<table");
    expect(html).toContain("Compare");
    expect(html).toContain("ou-card");
  });

  it("renders payload text inertly", () => {
    const html = render('root = Stack([Text("<script>alert(1)</script><img src=x onerror=alert(1)>"), Table(["<b>h</b>"], [["<iframe src=x></iframe>"]])])');
    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<iframe");
    expect(html).not.toContain("<b>");
    expect(html).toContain("&lt;script&gt;");
  });

  it("an invalid block shows its source as text, never blank and never a half-built UI", () => {
    const src = 'root = Stack([Text("ok"), Button("go", @OpenUrl("https://evil.test"))])';
    const html = render(src);
    expect(html).toContain("could not be displayed");
    expect(html).toContain("Showing its source");
    expect(html).not.toContain("<a ");
    expect(html).not.toContain("<button");
    expect(html).toContain("OpenUrl");
  });
});

describe("message body", () => {
  it("plain Markdown never touches OpenUI", () => {
    expect(renderToStaticMarkup(<MessageBody text="Just **text**" streaming={false} />)).toContain("<strong>text</strong>");
  });

  it("an unfinished block while streaming is a placeholder, not a UI", () => {
    const html = renderToStaticMarkup(<MessageBody text={'Here:\n```openui\nroot = Stack([Text("a"'} streaming />);
    expect(html).toContain("Preparing rich content");
    expect(html).not.toContain("Text(");
  });

  it("an unfinished block after the reply ended is shown as source", () => {
    const html = renderToStaticMarkup(<MessageBody text={'Here:\n```openui\nroot = Stack([Text("a"'} streaming={false} />);
    expect(html).toContain("Showing its source");
  });

  it("a complete block suspends until the chunk loads, and the text around it is already there", () => {
    const html = renderToStaticMarkup(<MessageBody text={"before\n```openui\n" + GOOD + "\n```\nafter"} streaming={false} />);
    expect(html).toContain("before");
    expect(html).toContain("after");
  });
});

describe("the OpenUI runtime does not phone home", () => {
  // @openuidev/lang-core carries opt-in usage telemetry (PostHog). It is off unless
  // OPENUI_RUNTIME_TELEMETRY_ENABLED is set, and CSP connect-src 'self' would block
  // it in a browser anyway. This pins the first half: sampling forced on, nothing is sent.
  it("sends nothing when parsing or rendering, even when sampling would pick the call", async () => {
    const fetchSpy = vi.fn(async () => new Response("{}"));
    vi.stubGlobal("fetch", fetchSpy);
    vi.spyOn(Math, "random").mockReturnValue(0);
    try {
      for (let i = 0; i < 25; i++) {
        check(GOOD);
        render(GOOD);
      }
      library.prompt({ preamble: "x" });
      await new Promise((r) => setTimeout(r, 20));
      expect(fetchSpy).not.toHaveBeenCalled();
    } finally {
      vi.restoreAllMocks();
      vi.unstubAllGlobals();
    }
  });
});

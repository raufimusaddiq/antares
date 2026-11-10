// @vitest-environment jsdom
import { getSchema } from "@tiptap/core";
import Image from "@tiptap/extension-image";
import StarterKit from "@tiptap/starter-kit";
import { Node } from "@tiptap/pm/model";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { partAnchors, relocate } from "./anchors";

// The same fixture as internal/blog/anchors_test.go: the Go rule (blocks, at publish) and this one (suggestions, in the editor) must agree.
const fixture = JSON.parse(readFileSync(resolve(process.cwd(), "../internal/blog/testdata/anchors.json"), "utf8")) as {
  documents: { name: string; doc: object; anchors: string[] }[];
  relocate: { name: string; anchors: string[]; anchor: string; hint: number; want: number }[];
};
const schema = getSchema([StarterKit.configure({ heading: { levels: [2, 3, 4] } }), Image]);

describe("anchors (shared fixture)", () => {
  it.each(fixture.documents)("the anchor of each part: $name", ({ doc, anchors }) => {
    expect(partAnchors(Node.fromJSON(schema, doc))).toEqual(anchors);
  });
  it.each(fixture.relocate)("a part is found again: $name", ({ anchors, anchor, hint, want }) => {
    expect(relocate(anchors, anchor, hint)).toBe(want);
  });
});

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
// @ts-expect-error -- a plain .mjs build script, no type declarations
import { generate, target } from "../../scripts/openui-prompt.mjs";

describe("the Visualize instruction", () => {
  it("is exactly OpenUI's chat prompt for the pinned library (run node web/scripts/openui-prompt.mjs after an upgrade)", () => {
    expect(readFileSync(target, "utf8")).toBe(generate());
  }, 60_000); // loads the whole component library
});

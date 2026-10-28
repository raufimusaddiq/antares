import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// Every class the tree puts on an element must have a rule in styles.css. Without this, deleting a rule is
// invisible: the markup keeps its className, the element silently falls through to a generic rule, and
// nothing in the suite notices (that is how .quiet was lost). Classes that are deliberately styleless go
// in STYLELESS with the reason.
const STYLELESS: Record<string, string> = {
  pending: "state hook on a user turn awaiting its server copy; styling comes from .turn.user",
};

const src = join(__dirname);
const css = readFileSync(join(src, "styles.css"), "utf8");

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx$/.test(name) && !/\.test\.tsx$/.test(name) ? [p] : [];
  });
}

/** The balanced `{...}` or quoted value after each `className=`. */
function classAttributes(code: string): string[] {
  const out: string[] = [];
  const re = /className=/g;
  for (let m = re.exec(code); m; m = re.exec(code)) {
    let i = m.index + m[0].length;
    if (code[i] === '"' || code[i] === "'") {
      out.push(code.slice(i + 1, code.indexOf(code[i], i + 1)));
    } else if (code[i] === "{") {
      let depth = 0;
      const start = i;
      for (; i < code.length; i++) {
        if (code[i] === "{") depth++;
        else if (code[i] === "}" && --depth === 0) break;
      }
      out.push(code.slice(start, i + 1));
    }
  }
  return out;
}

interface Used {
  exact: Set<string>;
  /** `run-${phase}` leaves the prefix "run-": some rule must start with it. */
  prefixes: Set<string>;
}

function usedClasses(): Used {
  const exact = new Set<string>();
  const prefixes = new Set<string>();
  for (const file of sources(src)) {
    for (const attr of classAttributes(readFileSync(file, "utf8"))) {
      // static text of every string and template literal in the attribute; `${...}` becomes a hole.
      // A literal compared with === or !== is a condition, not a class.
      const literals = [...attr.replace(/[!=]==\s*("[^"]*"|'[^']*')/g, "").matchAll(/"([^"]*)"|'([^']*)'|`([^`]*)`/g)].map((m) => m[1] ?? m[2] ?? m[3] ?? "");
      for (const lit of literals.length ? literals : [attr]) {
        for (const word of lit.split(/\s+/)) {
          const hole = word.indexOf("${");
          if (hole === 0 || word === "") continue;
          if (hole > 0) prefixes.add(word.slice(0, hole));
          else exact.add(word);
        }
      }
    }
  }
  return { exact, prefixes };
}

function styledClasses(): Set<string> {
  const text = css.replace(/\/\*[\s\S]*?\*\//g, "").replace(/url\([^)]*\)/g, "").replace(/"[^"]*"/g, "");
  return new Set([...text.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)].map((m) => m[1]));
}

describe("styles.css covers every className in the tree", () => {
  const { exact, prefixes } = usedClasses();
  const styled = styledClasses();

  it("finds the classes it is meant to check (the scan itself is not broken)", () => {
    expect(exact.size).toBeGreaterThan(30);
    for (const known of ["turn", "composer", "quiet", "notice"]) expect(exact.has(known)).toBe(true);
    expect(prefixes.has("run-")).toBe(true);
  });

  it("has a rule for every class used", () => {
    const missing = [...exact].filter((c) => !styled.has(c) && !(c in STYLELESS)).sort();
    expect(missing, `className used with no rule in styles.css: ${missing.join(", ")}`).toEqual([]);
  });

  it("has a rule for every dynamic class prefix", () => {
    const missing = [...prefixes].filter((p) => ![...styled].some((c) => c.startsWith(p))).sort();
    expect(missing, `dynamic className prefix with no rule in styles.css: ${missing.join(", ")}`).toEqual([]);
  });

  it("keeps the allowlist honest: every entry is still used and still styleless", () => {
    for (const c of Object.keys(STYLELESS)) {
      expect(exact.has(c), `${c} is allowlisted but no longer used`).toBe(true);
      expect(styled.has(c), `${c} now has a rule; remove it from STYLELESS`).toBe(false);
    }
  });
});

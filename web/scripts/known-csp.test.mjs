// Runnable regression for the narrow CSP exception of the image check (#139 review): both browser wordings are tolerated, nothing else is.
import { describe, expect, it } from "vitest";
import { knownBlobConsole, knownBlobEvent, unexpectedMessages } from "./known-csp.mjs";

const O = "http://127.0.0.1:45959";
const ID = "13bbd4ac-f531-4042-8b2d-31e15fe461d4";
const current = `Loading the image 'blob:${O}/${ID}' violates the following Content Security Policy directive: "img-src 'self'". The action has been blocked.`;
// Exactly as chrome-headless-shell 138.0.7204.94 (the CI pin) logs it, including the trailing newline.
const pinned138 = `Refused to load the image 'blob:${O}/${ID}' because it violates the following Content Security Policy directive: "img-src 'self'".\n`;
const pinned138NoNewline = pinned138.trimEnd();

describe("the known blob: thumbnail message", () => {
  it("matches the current Chromium wording", () => expect(knownBlobConsole(current, O)).toBe(true));
  it("matches the CI-pinned Chromium 138 wording, with and without its trailing newline", () => {
    expect(knownBlobConsole(pinned138, O)).toBe(true);
    expect(knownBlobConsole(pinned138NoNewline, O)).toBe(true);
  });
  it("matches the structured event forms", () => {
    for (const e of ["img-src blob", "img-src-elem blob", `img-src ${"blob:" + O + "/" + ID}`, `img-src-elem blob:${O}/${ID}`]) expect(knownBlobEvent(e, O), e).toBe(true);
  });
});

describe("unrelated violations are still rejected", () => {
  const bad = [
    `Refused to load the image 'https://evil.example/a.png' because it violates the following Content Security Policy directive: "img-src 'self'".`,
    `Refused to load the image 'data:image/png;base64,AAAA' because it violates the following Content Security Policy directive: "img-src 'self'".`,
    `Refused to load the image 'blob:http://other.example/${ID}' because it violates the following Content Security Policy directive: "img-src 'self'".`,
    `Refused to load the image 'blob:${O}/not-a-uuid' because it violates the following Content Security Policy directive: "img-src 'self'".`,
    `Refused to load the image 'blob:${O}/${ID}' because it violates the following Content Security Policy directive: "default-src 'none'".`,
    `Refused to load the image 'blob:${O}/${ID}' because it violates the following Content Security Policy directive: "media-src blob:".`,
    `Refused to load the script 'blob:${O}/${ID}' because it violates the following Content Security Policy directive: "script-src 'self'".`,
    `Refused to apply inline style because it violates the following Content Security Policy directive: "style-src 'self'".`,
    `Refused to frame 'https://evil.example/' because it violates the following Content Security Policy directive: "frame-src 'self'".`,
    `Loading the image 'blob:${O}/${ID}' violates the following Content Security Policy directive: "img-src 'self'". The action has been blocked. extra`,
    `prefix ${current}`,
    pinned138 + "\n\n",
    pinned138NoNewline + " ",
    pinned138NoNewline + " and more",
    "TypeError: x is not a function",
    "Failed to load resource: the server responded with a status of 500 (Internal Server Error)",
  ];
  for (const m of bad) it(`console: ${m.slice(0, 90)}`, () => expect(unexpectedMessages([m], [], O), m).toEqual([m]));
  for (const e of ["script-src blob", "img-src https://evil.example/a.png", "img-src data", "img-src-attr blob", "style-src inline", `img-src blob:http://other.example/${ID}`]) it(`event: ${e}`, () => expect(unexpectedMessages([], [e], O)).toEqual([e]));
  it("tolerates exactly the known messages and the deliberate 415, and reports a mix precisely", () => {
    const fourFifteen = "Failed to load resource: the server responded with a status of 415 (Unsupported Media Type)";
    expect(unexpectedMessages([current, pinned138, pinned138NoNewline, fourFifteen], ["img-src blob", "img-src-elem blob"], O)).toEqual([]);
    expect(unexpectedMessages([current, "boom"], ["img-src blob", "script-src blob"], O)).toEqual(["boom", "script-src blob"]);
  });
});

// The image check tolerates the deliberate 415 and nothing else (#142: the blob: thumbnail is allowed by policy now,
// so a blocked thumbnail is a failure again, like any other violation).
import { describe, expect, it } from "vitest";
import { unexpectedMessages } from "./known-csp.mjs";

const O = "http://127.0.0.1:45959";
const ID = "13bbd4ac-f531-4042-8b2d-31e15fe461d4";

describe("the image check's tolerance", () => {
  it("tolerates only the deliberate 415", () => {
    expect(unexpectedMessages(["Failed to load resource: the server responded with a status of 415 (Unsupported Media Type)"], [])).toEqual([]);
  });
  it("rejects every CSP message, the old blob: thumbnail one included, and any other error", () => {
    const bad = [
      `Refused to load the image 'blob:${O}/${ID}' because it violates the following Content Security Policy directive: "img-src 'self'".`,
      `Refused to load the image 'https://evil.example/a.png' because it violates the following Content Security Policy directive: "img-src 'self' blob:".`,
      `Refused to load the image 'data:image/png;base64,AAAA' because it violates the following Content Security Policy directive: "img-src 'self' blob:".`,
      "Failed to load resource: the server responded with a status of 500 (Internal Server Error)",
      "TypeError: x is not a function",
    ];
    for (const m of bad) expect(unexpectedMessages([m], []), m).toEqual([m]);
    expect(unexpectedMessages([], ["img-src blob"])).toEqual(["img-src blob"]);
  });
});

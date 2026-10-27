// @vitest-environment jsdom
// #150: "Sign out everywhere" ends the account's sessions on every device; plain "Sign out" ends only this one.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { signOut } from "./api";

const calls: string[] = [];
beforeEach(() => {
  calls.length = 0;
  vi.stubGlobal("navigator", {});
  vi.stubGlobal("fetch", vi.fn(async (path: string) => (calls.push(path), new Response(null, { status: 204 }))));
  localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("sign out everywhere (#150)", () => {
  it("posts to logout-all only when asked, and purges this device either way", async () => {
    localStorage.setItem("antares:draft:v1:a%40x.test:chat:c1", "unsent");
    expect(await signOut(true)).toBe(true);
    expect(calls).toEqual(["/api/auth/logout-all"]);
    expect(localStorage.getItem("antares:draft:v1:a%40x.test:chat:c1")).toBeNull();
    expect(await signOut()).toBe(true);
    expect(calls.at(-1)).toBe("/api/auth/logout");
  });
});

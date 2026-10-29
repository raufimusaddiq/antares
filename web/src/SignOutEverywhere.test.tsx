// @vitest-environment jsdom
// #150: "Sign out everywhere" ends the account's sessions on every device; plain "Sign out" ends only this one. A 401
// means "already signed out here", which is enough for a plain sign-out and not enough for everywhere.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { signOut } from "./api";
import { signOutWords } from "./App";

const calls: string[] = [];
const answer = (status: number) => vi.stubGlobal("fetch", vi.fn(async (path: string) => (calls.push(path), new Response(null, { status }))));
beforeEach(() => {
  calls.length = 0;
  vi.stubGlobal("navigator", {});
  localStorage.clear();
});
afterEach(() => {
  localStorage.clear();
});

describe("sign out everywhere (#150)", () => {
  it("posts to logout-all only when asked, and purges this device", async () => {
    answer(204);
    localStorage.setItem("antares:draft:v1:a%40x.test:chat:c1", "unsent");
    expect(await signOut(true)).toBe("ok");
    expect(calls).toEqual(["/api/auth/logout-all"]);
    expect(localStorage.getItem("antares:draft:v1:a%40x.test:chat:c1")).toBeNull();
    expect(await signOut()).toBe("ok");
    expect(calls.at(-1)).toBe("/api/auth/logout");
  });

  it("a 401 is success for a plain sign-out but not for everywhere: the other devices may still be signed in", async () => {
    answer(401);
    expect(await signOut()).toBe("ok");
    expect(await signOut(true)).toBe("expired");
    expect(signOutWords("expired", true)).toMatch(/other devices may still be signed in.*Sign in again/);
  });

  it("a failure is stated, and for everywhere says other devices may still be signed in", async () => {
    answer(500);
    expect(await signOut(true)).toBe("failed");
    expect(signOutWords("failed", true)).toMatch(/other devices may still be signed in/);
    expect(signOutWords("failed", false)).toBe("Could not sign out. Try again.");
    expect(signOutWords("ok", true)).toBe("");
  });
});

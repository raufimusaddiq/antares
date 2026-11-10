import { describe, expect, it } from "vitest";
import { canonicalUrl, destinations } from "./shareLinks";

describe("share links", () => {
  it("the canonical address is the origin and /b/slug, with no query or fragment", () => {
    expect(canonicalUrl("https://antares.example", "my-post")).toBe("https://antares.example/b/my-post");
    expect(canonicalUrl("https://antares.example/", "my-post")).toBe("https://antares.example/b/my-post");
  });

  // FR-003 and SC-005: whatever the destination, the address that arrives is the canonical one, untouched.
  it("every destination carries exactly the canonical address and the title, and nothing else", () => {
    const url = canonicalUrl("https://antares.example", "self-hosting-costs");
    const title = "Self-hosting: what it costs & why (2026) ?x=1#y";
    const ds = destinations(title, url);
    expect(ds.map((d) => d.id)).toEqual(["whatsapp", "x", "facebook", "linkedin", "telegram", "email"]);
    for (const d of ds) {
      const u = new URL(d.href);
      const params = u.searchParams;
      const found = [...params.values()];
      const carried = found.find((v) => v.includes("/b/self-hosting-costs"));
      expect(carried, d.id).toBeTruthy();
      // The address inside the destination is the canonical one: after the title for the services that take one text.
      const address = d.id === "whatsapp" ? carried!.slice(title.length + 1) : carried!;
      expect(address, d.id).toBe(url);
      // No other parameter than the title and the address.
      const names = [...params.keys()];
      expect(names.every((n) => ["text", "url", "u", "subject", "body"].includes(n)), `${d.id}: ${names.join(",")}`).toBe(true);
    }
    expect(new URL(ds[5].href).protocol).toBe("mailto:");
    expect(new URL(ds[1].href).searchParams.get("text")).toBe(title);
  });
});

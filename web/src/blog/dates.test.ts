import { describe, expect, it } from "vitest";
import { formatDay } from "./dates";

describe("formatDay", () => {
  it("shows the day as the design does, from the digits and not the viewer's time zone", () => {
    expect(formatDay("2026-11-08T23:59:59Z")).toBe("8 Nov 2026");
    expect(formatDay("2026-01-02T00:00:00Z", false)).toBe("2 Jan");
  });
  it("shows what is not an ISO date as it came", () => {
    expect(formatDay("")).toBe("");
    expect(formatDay("soon")).toBe("soon");
    expect(formatDay("2026-13-01")).toBe("2026-13-01");
  });
});

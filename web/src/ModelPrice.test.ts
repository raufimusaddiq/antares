import { describe, expect, it } from "vitest";
import { mediaPrice } from "./ModelPicker";
import type { ModelEntry } from "./chatApi";

const model = (output: string[], price: ModelEntry["price"]): ModelEntry => ({
  id: "m", available: true, hidden: false, reasoning: null, reasoningEfforts: [], modalities: { input: ["text"], output }, contextWindow: null, price,
});
const tokens = (free: boolean, v: number | null) => ({ source: "kenari", currency: "IDR", unit: "micro_idr_per_1m_tokens", free, input: v, output: v, cacheRead: null });

describe("media model prices", () => {
  it("never shows Kenari's per-token 0 as the price of an image or video model", () => {
    expect(mediaPrice(model(["image"], tokens(false, 0)))).toBe("Price: not in Kenari's API (see its catalogue)");
    expect(mediaPrice(model(["video"], tokens(false, 0)))).toBe("Price: not in Kenari's API (see its catalogue)");
  });
  it("keeps a published free flag, and leaves text models to their token price", () => {
    expect(mediaPrice(model(["image"], tokens(true, null)))).toBeNull(); // describePrice says "free"
    expect(mediaPrice(model(["text"], tokens(false, 5)))).toBeNull();
  });
});

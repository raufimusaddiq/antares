// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describeQuota, parseModels } from "./chatApi";
import { ModelPicker } from "./ModelPicker";

(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const catalog = {
  fetchedAt: "t",
  default: { provider: "alpha", model: "m-reason" },
  reasoningEfforts: ["low", "high"],
  providers: [
    {
      slug: "alpha", name: "Alpha", available: true,
      models: [
        { id: "m-reason", available: true, reasoning: true, serviceTier: true, price: null },
        { id: "m-plain", available: true, reasoning: false, serviceTier: null, price: null },
      ],
      quota: { state: "AVAILABLE", windows: [{ label: "5h", usedPercent: 0, resetsAt: "", scope: "account" }] },
    },
    { slug: "beta", name: "Beta", available: false, reason: "paste KEY", models: [{ id: "b1", available: false, reasoning: null, serviceTier: null, price: null }], quota: { state: "PROVIDER_USAGE_UNAVAILABLE" } },
  ],
};

describe("parseModels", () => {
  it("keeps unreported values null and unavailable models unavailable", () => {
    const c = parseModels(catalog)!;
    expect(c.providers[1].models[0]).toMatchObject({ available: false, reasoning: null, priority: null });
    expect(c.providers[0].models[1].priority).toBeNull();
    expect(c.providers[1].quota).toBeNull();
  });
  it("rejects what is not a catalog", () => {
    expect(parseModels({})).toBeNull();
    expect(parseModels(null)).toBeNull();
  });
  it("states missing provider usage and shows a real 0% as 0%", () => {
    const c = parseModels(catalog)!;
    expect(describeQuota(c.providers[1])[0]).toContain("unavailable");
    expect(describeQuota(c.providers[0])[0]).toContain("0% used");
  });
});

function respond(map: Record<string, unknown>) {
  vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${path.split("?")[0]}`;
    const body = key in map ? map[key] : null;
    return new Response(JSON.stringify(body), { status: body === null ? 404 : 200, headers: { "Content-Type": "application/json" } });
  }));
}

afterEach(() => vi.unstubAllGlobals());

describe("ModelPicker", () => {
  it("disables a model the assistant reports unavailable, and shows controls only where supported", async () => {
    respond({ "GET /api/models": catalog, "GET /api/chats/c1": { id: "c1", model: { provider: "alpha", model: "m-plain" } } });
    const el = document.createElement("div");
    document.body.append(el);
    await act(async () => createRoot(el).render(<ModelPicker chatId="c1" disabled={false} />));
    await act(async () => { el.querySelector("details")!.open = true; el.querySelector("details")!.dispatchEvent(new Event("toggle")); });
    const radios = [...el.querySelectorAll<HTMLInputElement>("input[type=radio]")];
    expect(radios.map((r) => [r.disabled, r.checked])).toEqual([[false, false], [false, true], [true, false]]);
    expect(el.textContent).toContain("Unavailable"); // context window, never inferred
    expect(el.querySelector("select")).toBeNull(); // m-plain lists no reasoning
    expect(el.querySelector("input[type=checkbox]")).toBeNull(); // and no priority tier
    expect(el.textContent).toContain("Provider usage: unavailable");
  });
});

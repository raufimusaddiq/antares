import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { AdminScreens, UsagePanel, microRp } from "./Admin";
import { getKenariUsage, parseKenariUsage } from "./adminApi";
import { parseAdminPath } from "./route";

// Shapes are GET /api/admin/kenari-usage as internal/auth/admin_usage.go writes them; every value is fabricated.
const ok = {
  state: "OK", source: "kenari.id: GET /v1/account/balance, /v1/account/quota, /v1/usage (30 days)", scope: "shared-account", fetchedAt: "2026-10-25T10:00:00Z", stale: false,
  balance: { state: "OK", data: { balanceMicroIdr: 25000000000, reservedMicroIdr: 1500000, availableMicroIdr: 24998500000, currency: "IDR" } },
  quota: { state: "OK", data: { alertPct: 80, coupon: null, plan: { name: "Indie", windows: [{ key: "week", budgetRp: 75000, usedRp: 12000, remainingRp: 63000, usedFrac: 0.16, resetsAt: "2026-10-30T07:12:00Z" }] } } },
  usage: { state: "OK", data: { start: 1788300000, end: 1790892000, rows: [{ model: "m-one", billingKind: "payg", requests: 120, promptTokens: 480000, completionTokens: 96000, cachedPromptTokens: 0, cacheWritePromptTokens: 0, costMicroIdr: 15400000, catalogMicroIdr: 15400000 }], total: { requests: 120, promptTokens: 480000, completionTokens: 96000, cachedPromptTokens: 0, cacheWritePromptTokens: 0, costMicroIdr: 15400000, catalogMicroIdr: 15400000 } } },
};
const panel = (v: unknown, busy = false) => renderToStaticMarkup(<UsagePanel v={parseKenariUsage(v)!} busy={busy} onRefresh={() => {}} />);

afterEach(() => vi.unstubAllGlobals());

describe("provider usage screen (#146)", () => {
  it("is an admin route and starts in a labelled loading state", () => {
    expect(parseAdminPath("/admin/usage")).toEqual({ kind: "usage" });
    const html = renderToStaticMarkup(<AdminScreens route={{ kind: "usage" }} onNavigate={() => {}} />);
    expect(html).toContain("Loading Provider usage");
    expect(html).toContain('aria-busy="true"');
  });

  it("shows provider figures as the shared account's, in Rupiah, apart from local estimates", () => {
    const html = panel(ok);
    expect(html).toContain("Shared provider account (kenari.id)");
    expect(html).toContain("none of this is a per-user or per-chat figure");
    expect(html).toContain("local estimates");
    expect(microRp(1500000).replace(/\s/g, " ")).toBe("Rp 1,5"); // a held Rp 1,50 is not rounded to Rp 2
    const plain = html.replace(/&nbsp;|\u00a0/g, " ");
    expect(plain).toContain("Rp 24.998,5");
    expect(plain).toContain("Rp 15,4");
    expect(plain).toContain("16% used · Rp 12.000 of Rp 75.000, Rp 63.000 left"); // the quota as a bar, with its amounts
    expect(html).toContain('role="meter" aria-label="Week quota"');
    expect(plain).toContain("480.000"); // token counts grouped: 480.000, not 480000
    expect(html).toContain("Whole provider account");
    expect(html).not.toContain("Stale");
  });

  it("states a stale read instead of passing it off as current", () => {
    expect(panel({ ...ok, stale: true })).toContain("Stale: the latest attempt to read Kenari failed");
  });

  it("not configured and unavailable show no number at all", () => {
    for (const state of ["NOT_CONFIGURED", "UNAVAILABLE"]) {
      const html = panel({ state, source: "x", balance: ok.balance });
      expect(html).not.toMatch(/Rp/);
      expect(html).not.toContain("Balance</h2>");
    }
    expect(panel({ state: "NOT_CONFIGURED" })).toContain("has not configured a provider key");
    expect(panel({ state: "UNAVAILABLE" })).toContain("no recent answer is held");
  });

  it("a refused section says so with the provider's code and has no figure", () => {
    const html = panel({ ...ok, balance: { state: "REFUSED", code: "restricted_key_not_allowed", data: null } });
    expect(html).toContain("Kenari refused this key (restricted_key_not_allowed)");
    expect(html).not.toContain(microRp(24998500000));
    expect(html).toContain("Plan quota"); // the other sections still render
  });

  it("an OK section with unreadable numbers is unavailable, never 0", () => {
    const bad = { ...ok, balance: { state: "OK", data: { balanceMicroIdr: 1, currency: "IDR" } } };
    const v = parseKenariUsage(bad)!;
    expect(v.balance?.state).toBe("UNAVAILABLE");
    expect(panel(bad)).toContain("Balance: Kenari did not answer. No figure is shown.");
    expect(parseKenariUsage({ state: "SURPRISE" })).toBeNull();
  });

  it("asks only for /api/admin/kenari-usage, with no body, key, profile or id", async () => {
    const f = vi.fn(async () => new Response(JSON.stringify(ok), { status: 200 }));
    vi.stubGlobal("fetch", f);
    const r = await getKenariUsage(true);
    expect(r.ok).toBe(true);
    const calls = f.mock.calls as unknown as [string, RequestInit][];
    expect(calls[0][0]).toBe("/api/admin/kenari-usage?refresh=1");
    expect(calls[0][1].method).toBe("GET");
    expect(calls[0][1].body).toBeUndefined();
  });
});

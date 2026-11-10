// Admin → Capabilities → Writing rules (specs/005-antislop-writing): whether the built-in writing rules are on, which version, and what the
// last 24 hours looked like. Counts only; the server never returns an account, a chat, a post or the text of the rules.

import { call, type AdminResult } from "./adminApi";

export interface WritingRules {
  enabled: boolean;
  version: string;
  source: string;
  license: string;
  last24h: { with: number; without: number; notApplied: number };
  /** "not_applied" when the rules are on and some run since the feature shipped went out without them. */
  warning: "" | "not_applied";
}

const obj = (v: unknown): Record<string, unknown> | null => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null);
const count = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null);

export function parseWritingRules(v: unknown): WritingRules | null {
  const o = obj(v);
  const c = obj(o?.last24h);
  if (!o || !c || typeof o.enabled !== "boolean" || typeof o.version !== "string" || typeof o.source !== "string" || typeof o.license !== "string") return null;
  const w = count(c.with), wo = count(c.without), na = count(c.notApplied);
  if (w === null || wo === null || na === null) return null;
  return { enabled: o.enabled, version: o.version, source: o.source, license: o.license, last24h: { with: w, without: wo, notApplied: na }, warning: o.warning === "not_applied" ? "not_applied" : "" };
}

export const getWritingRules = (signal?: AbortSignal) => call("GET", "/api/admin/writing-rules", parseWritingRules, undefined, signal);
export const setWritingRules = (enabled: boolean): Promise<AdminResult<WritingRules>> => call("PUT", "/api/admin/writing-rules", parseWritingRules, { enabled });

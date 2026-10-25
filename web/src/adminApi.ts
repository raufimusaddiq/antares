// The browser's whole view of the admin API. Every call is a cookie-authenticated
// request the server authorises; nothing here decides who may do what. Account
// data arrives as a closed set of plain strings (AdminUserView), and the only
// bodies sent are {} and {reason}: never a profile, key, route prefix or session id.

export interface Binding {
  status: string;
  verifiedAt: string;
}

export interface AdminUser {
  id: string;
  email: string;
  displayName: string;
  role: string;
  approvalStatus: string;
  tenantStatus: string;
  /** The tenant failure detail, exactly as the server recorded it. */
  tenantDetail: string;
  tenantAttempts: number;
  tenantCheckedAt: string;
  createdAt: string;
  approvedAt: string;
  lastLoginAt: string;
  binding: Binding | null;
}

export interface AuditEntry {
  id: string;
  actor: string;
  action: string;
  targetId: string;
  outcome: string;
  requestId: string;
  createdAt: string;
}

export type AdminResult<T> = { ok: true; value: T } | { ok: false; code: string; status: number };

export type Decision = "approve" | "reject" | "suspend" | "reactivate";
export type Requeue = "tenant-binding" | "tenant-verify";

export const USERS_PAGE = 200;
export const AUDIT_PAGE = 50;

const str = (v: unknown): string => (typeof v === "string" ? v : "");
const obj = (v: unknown): Record<string, unknown> | null => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null);

export function parseAdminUser(v: unknown): AdminUser | null {
  const o = obj(v);
  if (!o || !/^[0-9a-f]{32}$/.test(str(o.id))) return null;
  const b = obj(o.binding);
  return {
    id: str(o.id),
    email: str(o.email),
    displayName: str(o.displayName),
    role: str(o.role),
    approvalStatus: str(o.approvalStatus),
    tenantStatus: str(o.tenantStatus),
    tenantDetail: str(o.tenantDetail),
    tenantAttempts: typeof o.tenantAttempts === "number" ? o.tenantAttempts : 0,
    tenantCheckedAt: str(o.tenantCheckedAt),
    createdAt: str(o.createdAt),
    approvedAt: str(o.approvedAt),
    lastLoginAt: str(o.lastLoginAt),
    binding: b ? { status: str(b.status), verifiedAt: str(b.verifiedAt) } : null,
  };
}

function parseList<T>(key: string, one: (v: unknown) => T | null) {
  return (v: unknown): T[] | null => {
    const o = obj(v);
    if (!o || !Array.isArray(o[key])) return null;
    const out: T[] = [];
    for (const item of o[key] as unknown[]) {
      const p = one(item);
      if (p === null) return null; // a row we cannot read makes the page unreadable, never a shorter list
      out.push(p);
    }
    return out;
  };
}

export const parseUsers = parseList("users", parseAdminUser);

export function parseAuditEntry(v: unknown): AuditEntry | null {
  const o = obj(v);
  if (!o || !str(o.id)) return null;
  return { id: str(o.id), actor: str(o.actor), action: str(o.action), targetId: str(o.targetId), outcome: str(o.outcome), requestId: str(o.requestId), createdAt: str(o.createdAt) };
}

export interface AuditPage {
  entries: AuditEntry[];
  /** Cursor for the next (older) page; empty when this is the last one. The server owns its shape. */
  next: string;
}

export function parseAudit(v: unknown): AuditPage | null {
  const entries = parseList("entries", parseAuditEntry)(v);
  return entries ? { entries, next: str(obj(v)?.next) } : null;
}

async function call<T>(method: "GET" | "POST", path: string, parse: (b: unknown) => T | null, body?: unknown, signal?: AbortSignal): Promise<AdminResult<T>> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: body === undefined ? { Accept: "application/json" } : { Accept: "application/json", "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch {
    return { ok: false, code: "NETWORK", status: 0 };
  }
  let json: unknown = null;
  try {
    json = await res.json();
  } catch {
    // not JSON: handled below
  }
  if (!res.ok) {
    const e = obj(obj(json)?.error);
    return { ok: false, code: str(e?.code) || "INTERNAL", status: res.status };
  }
  const value = parse(json);
  return value === null ? { ok: false, code: "INTERNAL", status: res.status } : { ok: true, value };
}

/** `status` is an approval status the server filters on (SQL); `q` is a substring of name or email. Both optional. */
export function listUsers(offset: number, status: string, q: string, signal?: AbortSignal) {
  const p = new URLSearchParams({ limit: String(USERS_PAGE), offset: String(offset) });
  if (status) p.set("status", status);
  if (q) p.set("q", q);
  return call("GET", `/api/admin/users?${p}`, parseUsers, undefined, signal);
}
export const getUser = (id: string, signal?: AbortSignal) => call("GET", `/api/admin/users/${id}`, parseAdminUser, undefined, signal);
/** Keyset paging: `before` is the previous page's `next`, or empty for the newest entries. */
export function listAudit(before: string, signal?: AbortSignal) {
  const p = new URLSearchParams({ limit: String(AUDIT_PAGE) });
  if (before) p.set("before", before);
  return call("GET", `/api/admin/audit?${p}`, parseAudit, undefined, signal);
}
export const decide = (id: string, action: Decision, reason: string) => call("POST", `/api/admin/users/${id}/${action}`, parseAdminUser, reason ? { reason } : {});
export const requeue = (id: string, mode: Requeue) => call("POST", `/api/admin/users/${id}/${mode}`, parseAdminUser, {});

/** Words shown for a code. The server's own message is never displayed. */
export function describeAdminCode(code: string, status: number): string {
  if (status === 401 || code === "UNAUTHENTICATED") return "You are signed out. Sign in again.";
  switch (code) {
    case "ACCOUNT_PENDING":
    case "ACCOUNT_REJECTED":
    case "ACCOUNT_SUSPENDED":
    case "TENANT_NOT_READY":
      return "Your own account's state changed, so admin actions are no longer available to you. Sign in again.";
    case "NETWORK":
      return "Could not reach Antares. Check your connection.";
    case "NOT_FOUND":
      return "That account no longer exists.";
    case "REASON_REQUIRED":
      return "A reason is required for this action.";
    case "STATE_CONFLICT":
      return "The account's state changed, so that action is not allowed now. Reload to see its current state.";
    case "NO_BINDING":
      return "The account has no tenant binding to verify. Start provisioning instead.";
    case "FORBIDDEN":
      return "The server refused that action for this account (an administrator cannot do this to their own account).";
    case "RATE_LIMITED":
      return "Too many requests. Wait a moment and try again.";
    case "INVALID_REQUEST":
      return "The server refused that request. The reason may be too long or contain control characters.";
    default:
      return "Something went wrong. Nothing was changed that you can rely on: reload to see the current state.";
  }
}

/** On a screen load, 403 is its own state, not an error banner: signed in, but not allowed to administer. */
export const isForbidden = (r: { status: number }): boolean => r.status === 403;

// ---- shared provider account usage (#146) -----------------------------------------
// The admin-only read of the provider account. It is shared by every user: these are the provider's own
// figures for it, never one user's. A section the provider refused or could not answer has no numbers,
// and a number the server did not send is never drawn as 0.

export interface UsageSection<T> {
  state: "OK" | "REFUSED" | "UNAVAILABLE";
  code: string;
  data: T | null;
}
export interface KenariBalance { balanceMicroIdr: number; reservedMicroIdr: number; availableMicroIdr: number }
export interface KenariWindow { key: string; budgetRp: number; usedRp: number; remainingRp: number; usedFrac: number; resetsAt: string }
export interface KenariQuota {
  plan: { name: string; windows: KenariWindow[] } | null;
  coupon: { name: string; usedRp: number; remainingRp: number | null; expiresAt: string } | null;
  alertPct: number;
}
export interface KenariUsageRow { model: string; billingKind: string; requests: number; promptTokens: number; completionTokens: number; costMicroIdr: number; catalogMicroIdr: number }
export interface KenariUsage { start: number; end: number; rows: KenariUsageRow[]; total: KenariUsageRow }
export interface KenariUsageView {
  state: "OK" | "NOT_CONFIGURED" | "UNAVAILABLE";
  source: string;
  fetchedAt: string;
  stale: boolean;
  balance: UsageSection<KenariBalance> | null;
  quota: UsageSection<KenariQuota> | null;
  usage: UsageSection<KenariUsage> | null;
}

const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

function section<T>(v: unknown, parse: (d: Record<string, unknown>) => T | null): UsageSection<T> | null {
  const o = obj(v);
  if (!o) return null;
  const state = o.state === "OK" || o.state === "REFUSED" ? o.state : "UNAVAILABLE";
  const d = state === "OK" ? obj(o.data) : null;
  const data = d ? parse(d) : null;
  // an OK section whose numbers cannot be read is unavailable, not zero
  return { state: state === "OK" && data === null ? "UNAVAILABLE" : state, code: str(o.code), data };
}

function usageRow(v: unknown): KenariUsageRow | null {
  const o = obj(v);
  const r = o && [num(o.requests), num(o.promptTokens), num(o.completionTokens), num(o.costMicroIdr), num(o.catalogMicroIdr)];
  if (!o || !r || r.some((x) => x === null)) return null;
  return { model: str(o.model), billingKind: str(o.billingKind), requests: r[0]!, promptTokens: r[1]!, completionTokens: r[2]!, costMicroIdr: r[3]!, catalogMicroIdr: r[4]! };
}

export function parseKenariUsage(v: unknown): KenariUsageView | null {
  const o = obj(v);
  if (!o || (o.state !== "OK" && o.state !== "NOT_CONFIGURED" && o.state !== "UNAVAILABLE")) return null;
  if (o.state !== "OK") return { state: o.state, source: str(o.source), fetchedAt: "", stale: false, balance: null, quota: null, usage: null };
  return {
    state: "OK",
    source: str(o.source),
    fetchedAt: str(o.fetchedAt),
    stale: o.stale === true,
    balance: section(o.balance, (d) => {
      const b = [num(d.balanceMicroIdr), num(d.reservedMicroIdr), num(d.availableMicroIdr)];
      return b.some((x) => x === null) || d.currency !== "IDR" ? null : { balanceMicroIdr: b[0]!, reservedMicroIdr: b[1]!, availableMicroIdr: b[2]! };
    }),
    quota: section(o.quota, (d) => {
      const alert = num(d.alertPct);
      const p = obj(d.plan);
      const c = obj(d.coupon);
      if (alert === null) return null;
      const windows: KenariWindow[] = [];
      for (const w of Array.isArray(p?.windows) ? p.windows : []) {
        const wo = obj(w);
        const n = wo && [num(wo.budgetRp), num(wo.usedRp), num(wo.remainingRp), num(wo.usedFrac)];
        if (!wo || !n || n.some((x) => x === null)) return null;
        windows.push({ key: str(wo.key), budgetRp: n[0]!, usedRp: n[1]!, remainingRp: n[2]!, usedFrac: n[3]!, resetsAt: str(wo.resetsAt) });
      }
      const used = c ? num(c.usedRp) : null;
      if (c && used === null) return null;
      return { alertPct: alert, plan: p ? { name: str(p.name), windows } : null, coupon: c ? { name: str(c.name), usedRp: used!, remainingRp: num(c.remainingRp), expiresAt: str(c.expiresAt) } : null };
    }),
    usage: section(o.usage, (d) => {
      const start = num(d.start);
      const end = num(d.end);
      const total = usageRow(d.total);
      const rows = (Array.isArray(d.rows) ? d.rows : []).map(usageRow);
      return start === null || end === null || !total || rows.some((r) => r === null) ? null : { start, end, total, rows: rows as KenariUsageRow[] };
    }),
  };
}

export function getKenariUsage(refresh: boolean, signal?: AbortSignal) {
  return call("GET", `/api/admin/kenari-usage${refresh ? "?refresh=1" : ""}`, parseKenariUsage, undefined, signal);
}

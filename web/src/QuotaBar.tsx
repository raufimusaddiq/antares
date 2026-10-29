import { useEffect, useState } from "react";
import { getQuota, type SharedQuota } from "./chatApi";

// The shared provider plan's quota as bars (#139 follow-up): one plan serves every account, so its limit is
// everyone's. Admin → Provider usage shows the bars with Rupiah; every user sees them in the chat sidebar with the
// share used and the reset time only (GET /api/quota carries nothing else).

/** Whole numbers grouped the Indonesian way: 6.000.000. */
export const grouped = (n: number): string => new Intl.NumberFormat("id-ID").format(n);
const percent = new Intl.NumberFormat("id-ID", { style: "percent", maximumFractionDigits: 1 });

export const WINDOW_LABELS: Record<string, string> = { five_hour: "5 hours", week: "Week", month: "Month" };

/** From this share on, a window is shown as nearly spent (Kenari's own alert default is 80%). */
export const NEARLY_SPENT = 0.8;

/** "in 5 days", "in 3 hours", "in 12 minutes", or "soon". */
export function resetsIn(iso: string, now = Date.now()): string {
  const ms = Date.parse(iso) - now;
  if (!Number.isFinite(ms)) return "";
  if (ms <= 60_000) return "soon";
  const min = Math.round(ms / 60_000);
  if (min < 90) return `in ${min} minute${min === 1 ? "" : "s"}`;
  const h = Math.round(min / 60);
  if (h < 36) return `in ${h} hour${h === 1 ? "" : "s"}`;
  const d = Math.round(h / 24);
  return `in ${d} day${d === 1 ? "" : "s"}`;
}

/** One window's bar. detail is extra text after the share (the admin's Rupiah figures). */
export function QuotaBar({ label, frac, resetsAt, detail }: { label: string; frac: number; resetsAt: string; detail?: string }) {
  const f = Math.min(1, Math.max(0, frac));
  const pct = percent.format(f);
  const when = resetsIn(resetsAt);
  return (
    <div className={f >= NEARLY_SPENT ? "quota-row nearly" : "quota-row"}>
      <span className="quota-label">{label}</span>
      <span className="quota-pct">
        {pct} used{f >= NEARLY_SPENT ? ", nearly used up" : ""}{detail ? ` · ${detail}` : ""}
      </span>
      <div className="quota-track" role="meter" aria-label={`${label} quota`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(f * 1000) / 10} aria-valuetext={`${pct} used${when ? `, resets ${when}` : ""}`}>
        <div className="quota-fill" style={{ width: `${f * 100}%` }} />
      </div>
      {when && <span className="quota-reset">Resets {when}</span>}
    </div>
  );
}

const REFRESH_MS = 5 * 60_000;

/** The shared quota, read when the chat opens and every five minutes (the server answers from its cached copy). */
export function useSharedQuota(): SharedQuota | null {
  const [q, setQ] = useState<SharedQuota | null>(null);
  useEffect(() => {
    let live = true;
    const load = () => void getQuota().then((r) => live && r.ok && setQ(r.value));
    load();
    const t = window.setInterval(load, REFRESH_MS);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, []);
  return q;
}

/** The chat's view of the shared quota. Nothing is shown when the server has no plan figures to share. */
export function SharedQuotaMeter({ quota, className }: { quota: SharedQuota | null; className: string }) {
  if (!quota || quota.state !== "OK" || quota.windows.length === 0) return null;
  return (
    <section className={`quota-meter ${className}`} aria-label="Shared usage limit">
      <p className="quota-title" title="One plan serves everyone on this server, so this limit is shared by all accounts.">
        Shared limit, all users
      </p>
      {quota.windows.map((w) => (
        <QuotaBar key={w.key} label={WINDOW_LABELS[w.key] ?? w.key} frac={w.usedFrac} resetsAt={w.resetsAt} />
      ))}
    </section>
  );
}

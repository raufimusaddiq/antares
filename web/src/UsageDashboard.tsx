import { useEffect, useState } from "react";
import { describeCode, getUsageRange, type UsageRange, type UsageTotals } from "./chatApi";

// Usage dashboard (#151, PRD §21.7): the caller's own replies over 7, 30 or 90 UTC days, by model, by chat and by day.
// Tokens are what the assistant reported; replies that reported nothing are counted, never shown as zero. Rupiah
// figures are this app's estimate from its dated price list, not the provider's bill. Read only while open.

const num = (n: number) => n.toLocaleString("en-US");
/** Tokens of a group: unknown when it has replies and none of them reported usage, never 0. */
export const tokens = (t: UsageTotals) => (t.runs > 0 && t.unreported === t.runs ? "unknown" : num(t.input + t.output));
const rp = (n: number | null) => (n === null ? "—" : `≈ Rp ${Math.round(n).toLocaleString("en-US")}`);

function caveats(t: UsageTotals): string {
  const parts: string[] = [];
  if (t.unreported) parts.push(`${t.unreported} with no usage reported`);
  if (t.unpriced) parts.push(`${t.unpriced} on a model with no known price`);
  return parts.length ? ` (${parts.join(", ")}; not counted in the estimate)` : "";
}

export function UsageDashboard() {
  const [open, setOpen] = useState(false);
  const [days, setDays] = useState<7 | 30 | 90>(7);
  const [data, setData] = useState<UsageRange | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;
    let live = true;
    setData(null);
    setError("");
    void getUsageRange(days).then((r) => {
      if (!live) return;
      if (r.ok) setData(r.value);
      else setError(describeCode(r.code));
    });
    return () => {
      live = false;
    };
  }, [open, days]);

  const peak = data ? Math.max(1, ...data.byDay.map((d) => d.input + d.output)) : 1;

  return (
    <details className="usage-panel" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>Usage</summary>
      {open && (
        <>
          <div className="actions" role="group" aria-label="Range">
            {([7, 30, 90] as const).map((d) => (
              <button key={d} type="button" className={d === days ? "" : "quiet"} aria-pressed={d === days} onClick={() => setDays(d)}>
                {d} days
              </button>
            ))}
          </div>
          {error && <p role="alert">{error}</p>}
          {!data ? (
            !error && <p className="label">Loading…</p>
          ) : (
            <>
              <p>
                <strong>{tokens(data.total)} tokens</strong> in {num(data.total.runs)} replies since {data.since} (UTC) · estimated spend {rp(data.total.estimate)}
                {caveats(data.total)}
              </p>
              <p className="label">
                Estimates use the {data.currency} price list read on {data.priceDate}. They are not the provider's bill.
              </p>

              <h3>Tokens per day</h3>
              <div className="usage-bars" role="img" aria-label={`Tokens per day, ${data.byDay[0].day} to ${data.byDay[data.byDay.length - 1].day}; the table below has the figures`}>
                {data.byDay.map((d) => {
                  const t = d.input + d.output;
                  return (
                    <span key={d.day} className="usage-bar" title={`${d.day}: ${tokens(d)} tokens, ${num(d.runs)} replies, ${rp(d.estimate)}`}>
                      <span style={{ height: `${t === 0 ? 0 : Math.max(2, (t / peak) * 100)}%` }} />
                    </span>
                  );
                })}
              </div>
              <details>
                <summary>Per day as a table</summary>
                <table className="usage-table">
                  <thead>
                    <tr><th scope="col">Day (UTC)</th><th scope="col">Replies</th><th scope="col">Tokens</th><th scope="col">Estimate</th></tr>
                  </thead>
                  <tbody>
                    {data.byDay.map((d) => (
                      <tr key={d.day}><td>{d.day}</td><td>{num(d.runs)}</td><td>{tokens(d)}</td><td>{rp(d.estimate)}</td></tr>
                    ))}
                  </tbody>
                </table>
              </details>

              <h3>By model</h3>
              {data.byModel.length === 0 ? (
                <p className="label">No reported usage in this range.</p>
              ) : (
                <table className="usage-table">
                  <thead>
                    <tr><th scope="col">Model</th><th scope="col">Replies</th><th scope="col">Input</th><th scope="col">Output</th><th scope="col">Estimate</th></tr>
                  </thead>
                  <tbody>
                    {data.byModel.map((m) => (
                      <tr key={m.model}><td>{m.model || "not reported"}</td><td>{num(m.runs)}</td><td>{num(m.input)}</td><td>{num(m.output)}</td><td>{m.unpriced ? "no known price" : rp(m.estimate)}</td></tr>
                    ))}
                  </tbody>
                </table>
              )}

              <h3>By chat</h3>
              {data.byChat.length === 0 ? (
                <p className="label">No replies in this range.</p>
              ) : (
                <table className="usage-table">
                  <thead>
                    <tr><th scope="col">Chat</th><th scope="col">Replies</th><th scope="col">Tokens</th><th scope="col">Estimate</th></tr>
                  </thead>
                  <tbody>
                    {data.byChat.map((c) => (
                      <tr key={c.chatId || "deleted"}>
                        <td>{c.deleted ? "Deleted chats" : c.title || "Untitled"}</td>
                        <td>{num(c.runs)}{c.unreported ? ` (${c.unreported} unreported)` : ""}</td>
                        <td>{tokens(c)}</td>
                        <td>{rp(c.estimate)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </>
          )}
        </>
      )}
    </details>
  );
}

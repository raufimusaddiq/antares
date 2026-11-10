import { useState } from "react";
import { type AdminResult } from "./adminApi";
import { setWritingRules, type WritingRules } from "./adminWritingApi";

/**
 * The "Writing rules" card on Admin → Capabilities. One switch for everyone. A change applies to the next reply or post check and is
 * audited. The warning appears only when the rules are on and the server says some run went out without them.
 */
export function WritingRulesCard({ rules, onChanged, save = setWritingRules }: { rules: WritingRules; onChanged: (r: WritingRules) => void; save?: (enabled: boolean) => Promise<AdminResult<WritingRules>> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const toggle = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    const r = await save(!rules.enabled);
    setBusy(false);
    if (r.ok) onChanged(r.value);
    else setError("Could not save. Nothing was changed.");
  };
  const c = rules.last24h;
  return (
    <section className="notice-card writing-card" aria-labelledby="writing-h">
      <h2 id="writing-h">Writing rules</h2>
      {/* The whole label is the touch target. aria-disabled, not disabled, so the switch keeps keyboard focus while it saves. */}
      <label className="switch-cell">
        <input type="checkbox" role="switch" className="switch" checked={rules.enabled} aria-disabled={busy} onChange={() => void toggle()} />
        <span>{rules.enabled ? "Writing rules are on for everyone" : "Writing rules are off for everyone"}</span>
      </label>
      <p>
        <span className="chip chip-ai">writing-rules {rules.version}</span>
      </p>
      <p>When the assistant writes prose for a person to use (an email, a post, a description, a summary), it follows plain-writing rules: no filler or hype words, no em dashes, no invented numbers, quotes or sources. Answers, explanations, code and quotations are not changed.</p>
      <p className="label">
        Based on {rules.source}, {rules.license}.
      </p>
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
      {rules.warning === "not_applied" && (
        <p className="notice" role="alert">
          ⚠ The rules were not applied to {c.notApplied} {c.notApplied === 1 ? "run" : "runs"} since this feature shipped.
        </p>
      )}
      <p className="label">Last 24 hours · chat and post-check runs</p>
      <dl className="writing-counts">
        <div>
          <dt className="label">with the rules</dt>
          <dd>{c.with}</dd>
        </div>
        <div>
          <dt className="label">{rules.enabled ? "without" : "without (switched off)"}</dt>
          <dd>{c.without}</dd>
        </div>
      </dl>
      <p className="label">Titles and summaries are not counted. A change applies to the next reply or post check and is recorded in the audit log.</p>
    </section>
  );
}

import { useEffect, useRef, useState } from "react";
import { describeAdminCode } from "./adminApi";
import { Forbidden, Loading, Problem, useLoad } from "./adminLoad";
import { PURPOSES, PURPOSE_COPY, getModels, setBlocked, setDefaults, setMedia, type ModelRow, type ModelsView, type Purpose } from "./adminModelsApi";

// Admin → Models (specs/004-admin-full-control): a switch per model (off = nobody can choose or use it), a default per purpose, and
// what each model has been used for. The server decides what runs and whether a saved default still works; this screen only says so.

const WARNING: Record<string, string> = {
  not_listed: "Not offered any more. The service default is in use.",
  sunset: "Past its sunset date. The service default is in use.",
  off: "Switched off. The service default is in use.",
};

const num = (n: number) => new Intl.NumberFormat("en", { notation: n >= 1_000_000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(n);

/** The models a purpose can offer: the ones switched on and made for it, plus the saved choice, so the screen never shows a choice it is not making. */
export function offersFor(view: ModelsView, p: Purpose): { id: string; label: string }[] {
  const out = view.models.filter((m) => m.on && m.purposes.includes(p)).map((m) => ({ id: m.id, label: m.name }));
  const cur = view.purposes[p].current;
  if (cur && !out.some((o) => o.id === cur)) out.push({ id: cur, label: `${cur} (not offered now)` });
  return out;
}

function Defaults({ view, onSaved }: { view: ModelsView; onSaved: () => void }) {
  const initial = () => Object.fromEntries(PURPOSES.map((p) => [p, view.purposes[p].current])) as Record<Purpose, string>;
  const [draft, setDraft] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const changed = PURPOSES.filter((p) => draft[p] !== view.purposes[p].current);
  const save = async () => {
    setBusy(true);
    setError("");
    setSaved("");
    const text = changed.filter((p): p is "chat" | "summary" | "blog_assistant" => p === "chat" || p === "summary" || p === "blog_assistant");
    if (text.length > 0) {
      const r = await setDefaults(Object.fromEntries(text.map((p) => [p, draft[p]])));
      if (!r.ok) {
        setBusy(false);
        return setError(describeAdminCode(r.code, r.status));
      }
    }
    if (changed.includes("image") || changed.includes("video")) {
      const r = await setMedia(draft.image, draft.video);
      if (!r.ok) {
        setBusy(false);
        return setError(describeAdminCode(r.code, r.status));
      }
    }
    setBusy(false);
    setSaved("Saved.");
    onSaved();
  };
  return (
    <>
      <h2>Defaults</h2>
      <div className="models-card">
        {PURPOSES.map((p) => {
          const st = view.purposes[p];
          const warn = WARNING[st.warning] ?? "";
          const media = p === "image" || p === "video";
          return (
            <div key={p} className="purpose-row">
              <div className="purpose-label">
                <label htmlFor={`default-${p}`}>{PURPOSE_COPY[p].label}</label>
                <span className="label">{PURPOSE_COPY[p].hint}</span>
              </div>
              <select id={`default-${p}`} className={warn ? "invalid" : undefined} value={draft[p]} disabled={busy} onChange={(e) => setDraft((d) => ({ ...d, [p]: e.target.value }))}>
                <option value="">Service default</option>
                {offersFor(view, p).map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.label}
                  </option>
                ))}
              </select>
              <span className={warn ? "purpose-note warn" : "purpose-note"} role={warn ? "status" : undefined}>
                {warn ? `⚠ ${warn}` : media ? "Saving re-checks every account." : st.effective ? "In use" : "Service default in use"}
              </span>
            </div>
          );
        })}
      </div>
      <div className="actions">
        <button type="button" disabled={busy || changed.length === 0 || view.catalogue === "unavailable"} onClick={() => void save()}>
          {busy ? "Saving…" : "Save defaults"}
        </button>
        <span className="label" role="status">
          {saved || (changed.length > 0 ? "Not saved yet." : "A chat with a model of its own keeps it. Other chats follow the default at their next message.")}
        </span>
      </div>
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
    </>
  );
}

function Switch({ model, busy, onToggle }: { model: ModelRow; busy: boolean; onToggle: () => void }) {
  return (
    // A label around the switch and its word, so the whole thing is one touch target (44 px). aria-disabled, not disabled: a disabled
    // control loses keyboard focus, and the switch must keep it while the change is being saved.
    <label className="switch-cell">
      <input type="checkbox" role="switch" className="switch" checked={model.on} aria-disabled={busy} aria-label={`${model.name} for everyone`} onChange={() => !busy && onToggle()} />
      <span className={model.on ? undefined : "label"}>{model.on ? "On" : "Off"}</span>
    </label>
  );
}

function ModelTable({ view, onChanged }: { view: ModelsView; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [refused, setRefused] = useState<{ model: string; purposes: string[] } | null>(null);
  const [error, setError] = useState("");
  const alertRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (refused) alertRef.current?.focus();
  }, [refused]);
  const toggle = async (m: ModelRow) => {
    setBusy(true);
    setRefused(null);
    setError("");
    const off = view.models.filter((x) => !x.on).map((x) => x.id);
    const r = await setBlocked(m.on ? [...off, m.id] : off.filter((id) => id !== m.id));
    setBusy(false);
    if (r.ok) return onChanged();
    if (r.code === "MODEL_IS_DEFAULT") return setRefused({ model: m.name, purposes: (r.purposes ?? []).map((p) => PURPOSE_COPY[p as Purpose]?.label ?? p) });
    setError(describeAdminCode(r.code, r.status));
  };
  return (
    <>
      <h2>Models</h2>
      {refused && (
        <div ref={alertRef} tabIndex={-1} className="notice-card taken" role="alert">
          <h2>{refused.model} was not switched off.</h2>
          <p>
            It is the default for {refused.purposes.join(", ") || "a purpose"}. Choose another default first, then switch it off. Nothing was changed.
          </p>
        </div>
      )}
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
      {view.catalogue === "unavailable" && view.models.length === 0 ? (
        <div className="notice-card">
          <h2>The model list could not be read just now.</h2>
          <p>The current switches and defaults are kept. No new model can be chosen until the list is back; you can still go back to the service default.</p>
          <button type="button" className="quiet" onClick={onChanged}>
            Try again
          </button>
        </div>
      ) : (
        <div className="table-wrap">
          <table className="admin-table user-table" role="table">
            <thead role="rowgroup">
              <tr role="row">
                <th scope="col" role="columnheader">Model</th>
                <th scope="col" role="columnheader">Default for</th>
                <th scope="col" role="columnheader">Last 30 days</th>
                <th scope="col" role="columnheader">For everyone</th>
              </tr>
            </thead>
            <tbody role="rowgroup">
              {view.models.map((m) => (
                <tr key={m.id} role="row">
                  <th scope="row" role="rowheader">{m.name}</th>
                  <td role="cell">
                    <span className="cell-label">Default for</span>
                    {m.defaultFor.length > 0 ? m.defaultFor.map((p) => PURPOSE_COPY[p].label).join(", ") : "—"}
                  </td>
                  <td role="cell">
                    <span className="cell-label">Last 30 days</span>
                    {m.usage ? `${num(m.usage.calls)} calls · ${num(m.usage.tokens)} tokens` : "—"}
                  </td>
                  <td role="cell">
                    <span className="cell-label">For everyone</span>
                    <Switch model={m} busy={busy} onToggle={() => void toggle(m)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="label">
        Usage covers chat and post-check runs of the last 30 days, per model and never per person. Titles and summaries are not counted. A model that is a default cannot be switched off until another default is chosen.
      </p>
    </>
  );
}

export function ModelsPanel({ view, onChanged }: { view: ModelsView; onChanged: () => void }) {
  // A new read of the saved defaults starts the pickers from what the server now says.
  const key = PURPOSES.map((p) => view.purposes[p].current).join("|");
  return (
    <>
      <Defaults key={key} view={view} onSaved={onChanged} />
      <ModelTable view={view} onChanged={onChanged} />
    </>
  );
}

export function AdminModelsScreen() {
  const [state, reload] = useLoad((s) => getModels(s), "models");
  return (
    <section aria-labelledby="models-h">
      <h1 id="models-h">Models</h1>
      <p className="label">Switch a model off and nobody can choose or use it, for chat, summaries and titles, the blog assistant, images and videos. Defaults apply to everyone who has not chosen otherwise.</p>
      {state.kind === "loading" && <Loading what="models" />}
      {state.kind === "forbidden" && <Forbidden />}
      {state.kind === "error" && <Problem message={state.message} onRetry={reload} />}
      {state.kind === "ready" && <ModelsPanel view={state.value} onChanged={reload} />}
    </section>
  );
}

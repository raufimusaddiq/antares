import { useEffect, useMemo, useState } from "react";
import { describeCode, describeQuota, getChatModel, getModels, putChatModel, type ModelCatalog, type ModelChoice } from "./chatApi";

/**
 * Model picker for one chat (PRD LLM-001..007, CTX-004). The list is the assistant's own
 * inventory for this account; a model it reports unavailable is shown but cannot be chosen,
 * reasoning and priority controls appear only where the inventory says they are supported,
 * and anything it did not report reads "Unavailable".
 */
export function ModelPicker({ chatId, disabled }: { chatId: string; disabled: boolean }) {
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null);
  const [choice, setChoice] = useState<ModelChoice | null | undefined>(undefined); // undefined = not read yet
  const [query, setQuery] = useState("");
  const [msg, setMsg] = useState("");

  useEffect(() => {
    let live = true;
    setChoice(undefined);
    setMsg("");
    void getChatModel(chatId).then((r) => live && setChoice(r.ok ? r.value.choice : undefined));
    return () => {
      live = false;
    };
  }, [chatId]);

  const load = (refresh: boolean) =>
    void getModels(refresh).then((r) => {
      setCatalog(r.ok ? r.value : null);
      setMsg(r.ok ? "" : describeCode(r.code));
    });

  const save = async (body: Parameters<typeof putChatModel>[1]) => {
    const r = await putChatModel(chatId, body);
    if (r.ok) {
      setChoice(r.value.choice);
      setMsg("");
    } else setMsg(describeCode(r.code));
  };

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (catalog?.providers ?? []).map((p) => ({ ...p, models: p.models.filter((m) => !q || `${p.name} ${m.id}`.toLowerCase().includes(q)) }));
  }, [catalog, query]);

  const label = choice === undefined ? "Model" : choice ? `Model: ${choice.model}` : catalog?.default ? `Model: ${catalog.default.model} (default)` : "Model: default";
  const current = catalog?.providers.find((p) => p.slug === choice?.provider)?.models.find((m) => m.id === choice?.model);

  return (
    <details className="usage" onToggle={(e) => e.currentTarget.open && load(true)}>
      <summary>{label}</summary>
      {msg && <p role="alert">{msg}</p>}
      {!catalog && !msg && <p>Loading models…</p>}
      {catalog && (
        <>
          <label>
            Search models <input type="search" value={query} onChange={(e) => setQuery(e.target.value)} />
          </label>
          {disabled && <p>A reply is running. The choice applies to your next message.</p>}
          {shown.map((p) => (
            <fieldset key={p.slug}>
              <legend>
                {p.name}
                {!p.available && " (unavailable)"}
              </legend>
              {!p.available && p.reason && <p>{p.reason}</p>}
              {p.models.map((m) => (
                <div key={m.id}>
                  <label>
                    <input
                      type="radio"
                      name="model"
                      disabled={!m.available}
                      checked={choice?.provider === p.slug && choice.model === m.id}
                      onChange={() => void save({ provider: p.slug, model: m.id })}
                    />{" "}
                    {m.id}
                    {!m.available && " (unavailable)"}
                  </label>{" "}
                  <small>
                    Reasoning: {m.reasoning === null ? "Unavailable" : m.reasoning ? "yes" : "no"} · Priority tier: {m.priority === null ? "Unavailable" : m.priority ? "yes" : "no"} · Context window: Unavailable · Price:{" "}
                    {m.price ? (m.price.free ? "free" : `in ${m.price.input || "Unavailable"}, out ${m.price.output || "Unavailable"}, cache ${m.price.cache || "Unavailable"}`) : "Unavailable"}
                  </small>
                </div>
              ))}
              <ul>
                {describeQuota(p).map((l) => (
                  <li key={l}>{l}</li>
                ))}
              </ul>
            </fieldset>
          ))}
          {choice && current && (
            <div>
              {current.reasoning === true && (
                <label>
                  Reasoning effort{" "}
                  <select
                    value={choice.reasoningEffort}
                    onChange={(e) => void save({ provider: choice.provider, model: choice.model, reasoningEffort: e.target.value || undefined, serviceTier: choice.serviceTier || undefined })}
                  >
                    <option value="">Default</option>
                    {catalog.reasoningEfforts.map((x) => (
                      <option key={x} value={x}>
                        {x}
                      </option>
                    ))}
                  </select>
                </label>
              )}{" "}
              {current.priority === true && (
                <label>
                  <input
                    type="checkbox"
                    checked={choice.serviceTier === "priority"}
                    onChange={(e) => void save({ provider: choice.provider, model: choice.model, reasoningEffort: choice.reasoningEffort || undefined, serviceTier: e.target.checked ? "priority" : undefined })}
                  />{" "}
                  Priority tier
                </label>
              )}
              <p>
                <small>Effort levels are the ones the assistant accepts; it does not report which a model supports, so the provider may adjust one.</small>
              </p>
            </div>
          )}
          {choice && <button type="button" onClick={() => void save({ reset: true })}>Use the default model</button>}
        </>
      )}
    </details>
  );
}

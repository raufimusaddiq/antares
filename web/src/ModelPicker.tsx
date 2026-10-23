import { useEffect, useMemo, useRef, useState } from "react";
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

  // Every response is applied only if it belongs to this chat and is the newest request for it. A save
  // that resolves after the chat changed, or after a newer save or read, is dropped: it must never show
  // as another chat's model, nor be sent back as that chat's provider/model.
  const gen = useRef(0);
  const chat = useRef(chatId);
  chat.current = chatId;
  // The choice the next change builds on. It moves when a change is requested (not when its response
  // arrives), so two quick changes compose instead of the second overwriting the first from stale state.
  const want = useRef<ModelChoice | null | undefined>(undefined);
  // Saves for one chat go out one at a time, in the order they were made, so the server's last write is the user's last.
  const tail = useRef<Promise<unknown>>(Promise.resolve());

  useEffect(() => {
    gen.current += 1;
    const mine = gen.current;
    want.current = undefined;
    tail.current = Promise.resolve();
    setChoice(undefined);
    setMsg("");
    void getChatModel(chatId).then((r) => {
      if (gen.current !== mine || chat.current !== chatId) return;
      want.current = r.ok ? r.value.choice : undefined;
      setChoice(want.current);
    });
    return () => {
      gen.current += 1; // anything still in flight for the old chat is now stale
    };
  }, [chatId]);

  const load = (refresh: boolean) =>
    void getModels(refresh).then((r) => {
      setCatalog(r.ok ? r.value : null);
      setMsg(r.ok ? "" : describeCode(r.code));
    });

  /** next = the choice after this change, or null to reset. */
  const change = (next: ModelChoice | null) => {
    const forChat = chatId;
    want.current = next;
    gen.current += 1;
    const mine = gen.current;
    const body = next
      ? { provider: next.provider, model: next.model, reasoningEffort: next.reasoningEffort || undefined, serviceTier: next.serviceTier || undefined }
      : { reset: true as const };
    tail.current = tail.current.then(async () => {
      const r = await putChatModel(forChat, body);
      if (gen.current !== mine || chat.current !== forChat) return; // stale: another save, a read or a chat switch came after
      if (r.ok) {
        want.current = r.value.choice;
        setChoice(r.value.choice);
        setMsg("");
      } else {
        setMsg(describeCode(r.code));
        const back = await getChatModel(forChat); // the failed change is not the saved one: show what the server holds
        if (gen.current === mine && chat.current === forChat && back.ok) {
          want.current = back.value.choice;
          setChoice(back.value.choice);
        }
      }
    });
  };
  const patch = (p: Partial<ModelChoice>) => want.current && change({ ...want.current, ...p });

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
                      onChange={() => change({ provider: p.slug, model: m.id, reasoningEffort: "", serviceTier: "" })}
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
                    onChange={(e) => patch({ reasoningEffort: e.target.value })}
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
                    onChange={(e) => patch({ serviceTier: e.target.checked ? "priority" : "" })}
                  />{" "}
                  Priority tier
                </label>
              )}
              <p>
                <small>Effort levels are the ones the assistant accepts; it does not report which a model supports, so the provider may adjust one.</small>
              </p>
            </div>
          )}
          {choice && <button type="button" onClick={() => change(null)}>Use the default model</button>}
        </>
      )}
    </details>
  );
}

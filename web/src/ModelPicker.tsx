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
  // Writes for a chat go out one at a time, in the order made, through a queue that belongs to the CHAT
  // and outlives chat switches (A -> B -> A reuses A's queue), so a write still queued for A can never be
  // overtaken by a later write for A. Each write carries the whole choice, so a write that a newer one for
  // the same chat has superseded before it was dispatched is skipped: the newest write is what the server keeps.
  const queues = useRef(new Map<string, Promise<unknown>>());
  const newest = useRef(new Map<string, number>());
  const counter = useRef(0);
  const enqueue = (forChat: string, job: () => Promise<void>) => {
    queues.current.set(forChat, (queues.current.get(forChat) ?? Promise.resolve()).then(job));
  };

  useEffect(() => {
    gen.current += 1;
    const mine = gen.current;
    want.current = undefined;
    setChoice(undefined);
    setMsg("");
    // The read waits behind this chat's own pending writes, so it reports the choice they leave.
    enqueue(chatId, async () => {
      const r = await getChatModel(chatId);
      if (gen.current !== mine || chat.current !== chatId) return;
      want.current = r.ok ? r.value.choice : undefined;
      setChoice(want.current);
    });
    return () => {
      gen.current += 1; // a read still in flight for the old chat is now stale
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
    const seq = ++counter.current;
    newest.current.set(forChat, seq);
    const body = next
      ? { provider: next.provider, model: next.model, reasoningEffort: next.reasoningEffort || undefined, serviceTier: next.serviceTier || undefined }
      : { reset: true as const };
    const current = () => newest.current.get(forChat) === seq && chat.current === forChat;
    enqueue(forChat, async () => {
      if (newest.current.get(forChat) !== seq) return; // superseded before it was sent
      const r = await putChatModel(forChat, body);
      if (!current()) return; // a newer write for this chat exists, or the chat is not on screen
      if (r.ok) {
        want.current = r.value.choice;
        setChoice(r.value.choice);
        setMsg("");
      } else {
        setMsg(describeCode(r.code));
        const back = await getChatModel(forChat); // the failed change is not the saved one: show what the server holds
        if (current() && back.ok) {
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

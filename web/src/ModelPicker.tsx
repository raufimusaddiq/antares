import { useEffect, useMemo, useRef, useState } from "react";
import { describeCode, describePrice, getChatModel, getModels, getModelVisibility, putChatModel, putModelVisibility, type ModelCatalog, type ModelChoice, type ModelEntry } from "./chatApi";

/**
 * Model picker for one chat (PRD LLM-001..007, CTX-004, issue #115). The models and their metadata
 * come from the Kenari catalogue; a model whose provider the assistant cannot use is shown but cannot
 * be chosen, the reasoning control offers only the levels the catalogue publishes for that model and
 * the assistant accepts, a service tier is never offered (the reason is shown), and anything the
 * catalogue did not publish reads "unknown". Hiding a model changes only this account's own list.
 */
export function ModelPicker({ chatId, disabled, onChanged }: { chatId: string; disabled: boolean; onChanged?: () => void }) {
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null);
  const [choice, setChoice] = useState<ModelChoice | null | undefined>(undefined); // undefined = not read yet
  const [query, setQuery] = useState("");
  const [msg, setMsg] = useState("");
  const [showHidden, setShowHidden] = useState(false);
  // A catalogue read that failed (the list on screen, if any, is the earlier one) and a chat setting that could not be read.
  const [loadFailed, setLoadFailed] = useState(false);
  const [loading, setLoading] = useState(false);
  const [readFailed, setReadFailed] = useState(false);
  const [readTick, setReadTick] = useState(0);
  // Only the newest catalogue request may answer: an older one that lands last must not replace it.
  const loadSeq = useRef(0);

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
    setReadFailed(false);
    setMsg("");
    // The read waits behind this chat's own pending writes, so it reports the choice they leave.
    enqueue(chatId, async () => {
      const r = await getChatModel(chatId);
      if (gen.current !== mine || chat.current !== chatId) return;
      want.current = r.ok ? r.value.choice : undefined;
      setChoice(want.current);
      setReadFailed(!r.ok);
    });
    return () => {
      gen.current += 1; // a read still in flight for the old chat is now stale
    };
  }, [chatId, readTick]);

  const load = (refresh: boolean) => {
    const mine = ++loadSeq.current;
    setLoading(true);
    void getModels(refresh).then((r) => {
      if (mine !== loadSeq.current) return; // a newer request owns the answer
      setLoading(false);
      setLoadFailed(!r.ok);
      if (r.ok) {
        if (hiddenPending.current === 0) hiddenWant.current = new Set(r.value.providers.flatMap((p) => p.models).filter((m) => m.hidden).map((m) => m.id));
        setCatalog(hiddenWant.current && hiddenPending.current > 0 ? withHidden(r.value, hiddenWant.current) : r.value);
        setMsg("");
      } else {
        // The list already on screen stays choosable (the server validates every pick); the failure is stated, never hidden.
        setMsg(describeCode(r.code));
      }
    });
  };

  /** next = the choice after this change, or null to reset. */
  const change = (next: ModelChoice | null) => {
    const forChat = chatId;
    want.current = next;
    gen.current += 1;
    const seq = ++counter.current;
    newest.current.set(forChat, seq);
    const body = next ? { model: next.model, reasoningEffort: next.reasoningEffort || undefined } : { reset: true as const };
    const current = () => newest.current.get(forChat) === seq && chat.current === forChat;
    enqueue(forChat, async () => {
      if (newest.current.get(forChat) !== seq) return; // superseded before it was sent
      const r = await putChatModel(forChat, body);
      if (!current()) return; // a newer write for this chat exists, or the chat is not on screen
      if (r.ok) {
        want.current = r.value.choice;
        setChoice(r.value.choice);
        setMsg("");
        onChanged?.(); // what the chat can accept (images) follows its model
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

  // The account's hidden set. Every change updates the intended set at once and the PUTs go out one at
  // a time, each carrying the intended set as it is when it is sent, so two quick changes compose
  // (hide A then B keeps both) and a late answer for the first cannot bring A back. The set belongs to
  // the account, not to a chat, so a chat switch does not touch it.
  const hiddenWant = useRef<Set<string> | null>(null);
  const hiddenQueue = useRef<Promise<unknown>>(Promise.resolve());
  const hiddenPending = useRef(0);
  const showHiddenSet = (ids: Set<string>) => setCatalog((c) => c && withHidden(c, ids));

  const setHidden = (id: string, hide: boolean) => {
    const next = new Set(hiddenWant.current ?? (catalog?.providers ?? []).flatMap((p) => p.models).filter((m) => m.hidden).map((m) => m.id));
    if (hide) next.add(id);
    else next.delete(id);
    hiddenWant.current = next;
    showHiddenSet(next);
    hiddenPending.current += 1;
    hiddenQueue.current = hiddenQueue.current.then(async () => {
      const r = await putModelVisibility([...(hiddenWant.current ?? next)]);
      hiddenPending.current -= 1;
      if (r.ok) {
        setMsg("");
        if (hiddenPending.current === 0) {
          hiddenWant.current = new Set(r.value.hidden);
          showHiddenSet(hiddenWant.current);
        }
        return;
      }
      setMsg(describeCode(r.code));
      if (hiddenPending.current === 0) {
        // Show what the server holds, not the change that failed.
        const back = await getModelVisibility();
        if (back.ok && hiddenPending.current === 0) {
          hiddenWant.current = new Set(back.value.hidden);
          showHiddenSet(hiddenWant.current);
        }
      }
    });
  };

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (catalog?.providers ?? []).map((p) => ({ ...p, models: p.models.filter((m) => (showHidden || !m.hidden || m.id === choice?.model) && (!q || `${p.name} ${m.id}`.toLowerCase().includes(q))) }));
  }, [catalog, query, showHidden, choice?.model]);

  const label = choice === undefined ? "Model" : choice ? `Model: ${choice.model}` : catalog?.default ? `Model: ${catalog.default.model} (default)` : "Model: default";
  const current = catalog?.providers.flatMap((p) => p.models).find((m) => m.id === choice?.model);

  return (
    <details className="usage" onToggle={(e) => e.currentTarget.open && load(true)}>
      <summary>{label}</summary>
      {readFailed && (
        <p role="alert">
          This chat's model could not be read, so the choice shown may not be the saved one.{" "}
          <button type="button" onClick={() => setReadTick((n) => n + 1)}>Retry</button>
        </p>
      )}
      {msg && (
        <p role="alert">
          {msg}
          {catalog && loadFailed ? " The list below may be out of date." : ""}{" "}
          {loadFailed && (
            <button type="button" disabled={loading} onClick={() => load(true)}>Retry</button>
          )}
        </p>
      )}
      {!catalog && !msg && <p>Loading models…</p>}
      {catalog && (
        <>
          <label>
            Search models <input type="search" value={query} onChange={(e) => setQuery(e.target.value)} />
          </label>
          <label>
            <input type="checkbox" checked={showHidden} onChange={(e) => setShowHidden(e.target.checked)} /> Show hidden models
          </label>
          {disabled && <p>A reply is running. The choice applies to your next message.</p>}
          {catalog.fetchedAt && (
            <p>
              <small>
                Catalogue read from Kenari {catalog.fetchedAt}
                {catalog.stale ? ". It could not be refreshed, so it may be out of date." : "."}
              </small>
            </p>
          )}
          {catalog.providers.every((p) => p.models.length === 0) && <p>The catalogue lists no models for this assistant.</p>}
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
                      checked={choice?.model === m.id}
                      onChange={() => change({ provider: p.slug, model: m.id, reasoningEffort: "", serviceTier: "" })}
                    />{" "}
                    {m.id}
                    {!m.available && " (unavailable)"}
                  </label>{" "}
                  <button type="button" aria-label={`${m.hidden ? "Show" : "Hide"} ${m.id}`} onClick={() => setHidden(m.id, !m.hidden)}>
                    {m.hidden ? "Show" : "Hide"}
                  </button>{" "}
                  <small>{badges(m)}</small>
                </div>
              ))}
            </fieldset>
          ))}
          {choice && current && (
            <div>
              {current.reasoningEfforts.length > 0 && (
                <label>
                  Reasoning effort{" "}
                  <select
                    value={choice.reasoningEffort}
                    onChange={(e) => patch({ reasoningEffort: e.target.value })}
                  >
                    <option value="">Default</option>
                    {current.reasoningEfforts.map((x) => (
                      <option key={x} value={x}>
                        {x}
                      </option>
                    ))}
                  </select>
                </label>
              )}{" "}
              <p>
                <small>
                  {current.reasoning === true && current.reasoningEfforts.length === 0 ? "This model reasons, but the catalogue does not publish its levels, so none can be chosen. " : ""}
                  Service tier: not offered{catalog.unsupported.serviceTier ? ` (${catalog.unsupported.serviceTier})` : ""}.
                </small>
              </p>
            </div>
          )}
          {choice && <button type="button" onClick={() => change(null)}>Use the default model</button>}
        </>
      )}
    </details>
  );
}

/** One line of metadata for a model; anything the catalogue did not publish is "unknown". */
function badges(m: ModelEntry): string {
  const kinds = (l: string[]) => (l.length ? l.join(", ") : "unknown");
  return [
    `Input: ${m.modalities ? kinds(m.modalities.input) : "unknown"}`,
    `Output: ${m.modalities ? kinds(m.modalities.output) : "unknown"}`,
    `Reasoning: ${m.reasoning === null ? "unknown" : m.reasoning ? "yes" : "no"}`,
    `Context window: ${m.contextWindow === null ? "unknown" : `${m.contextWindow.toLocaleString("en-US")} tokens`}`,
    describePrice(m.price),
  ].join(" · ");
}

const withHidden = (c: ModelCatalog, ids: Set<string>): ModelCatalog => ({
  ...c,
  providers: c.providers.map((p) => ({ ...p, models: p.models.map((m) => ({ ...m, hidden: ids.has(m.id) })) })),
});

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { answerRun, approveRun, describeCode, getRunPrompts, type RunPrompt } from "./chatApi";

// The decisions the assistant is waiting for on the open run. The list is read from the server (the
// database is the authority), so a reload shows the same cards; nothing here approves anything by itself,
// and there is no timer, default button or "always allow". The run does not continue until a person clicks.
export function RunPrompts({ runId, tick }: { runId: string; tick: number }) {
  const [prompts, setPrompts] = useState<RunPrompt[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  const load = useCallback(async () => {
    const r = await getRunPrompts(runId);
    if (r.ok) {
      setPrompts(r.value);
      setError("");
    } else setError(describeCode(r.code));
  }, [runId]);

  useEffect(() => {
    void load();
  }, [load, tick]);

  const send = async (p: RunPrompt, act: () => ReturnType<typeof approveRun>) => {
    setBusy(p.promptId);
    const r = await act();
    setBusy("");
    setError(r.ok ? "" : describeCode(r.code));
    await load();
  };

  if (prompts.length === 0 && !error) return null;
  return (
    <div className="run-prompts">
      {prompts.map((p) =>
        p.kind === "approval" ? (
          <section key={p.promptId} className="notice" role="group" aria-label="Approval needed">
            <h2 className="who">Approval needed</h2>
            <p>The assistant is paused until you decide.</p>
            <pre className="plain">{p.text}</pre>
            {p.detail && <p className="label">{p.detail}</p>}
            <div className="actions">
              <button type="button" disabled={busy === p.promptId} onClick={() => void send(p, () => approveRun(runId, p.promptId, "once"))}>
                Allow once
              </button>{" "}
              <button type="button" className="quiet" disabled={busy === p.promptId} onClick={() => void send(p, () => approveRun(runId, p.promptId, "deny"))}>
                Deny
              </button>
            </div>
          </section>
        ) : (
          <Question key={p.promptId} p={p} busy={busy === p.promptId} onAnswer={(a) => void send(p, () => answerRun(runId, p.promptId, a))} />
        ),
      )}
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

function Question({ p, busy, onAnswer }: { p: RunPrompt; busy: boolean; onAnswer: (a: string) => void }) {
  const [text, setText] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const a = text.trim();
    if (a) onAnswer(a);
  };
  return (
    <form className="notice" aria-label="The assistant has a question" onSubmit={submit}>
      <h2 className="who">Question from the assistant</h2>
      <p className="plain">{p.text}</p>
      {p.choices.length > 0 && (
        <div className="actions">
          {p.choices.map((c) => (
            <button key={c} type="button" className="quiet" disabled={busy} onClick={() => onAnswer(c)}>
              {c}
            </button>
          ))}
        </div>
      )}
      <label>
        <span className="label">Your answer</span>
        <input value={text} maxLength={4000} onChange={(e) => setText(e.target.value)} disabled={busy} />
      </label>{" "}
      <button type="submit" disabled={busy || text.trim() === ""}>
        Answer
      </button>
    </form>
  );
}

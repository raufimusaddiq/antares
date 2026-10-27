import { useCallback, useEffect, useRef, useState } from "react";
import { describeCode, editQueued, getQueue, pauseQueue, removeQueued, sendQueuedNext, type ChatQueue, type QueuedMessage } from "./chatApi";

// Messages waiting for the conversation's current reply to end (PRD §27.4, issue #110). The list is read
// from the server, so a reload or another device shows the same queue. A queued message is a new turn sent
// after the reply ends; it is never guidance into the running reply (that is Steer).

/** Why the queue is not sending on its own, in words. */
export function describePause(reason: string): string {
  switch (reason) {
    case "":
      return "";
    case "USER":
      return "Paused by you.";
    case "FAILED":
    case "TIMED_OUT":
      return "Paused because the last reply did not finish.";
    case "CANCELLED":
      return "Paused because the last reply was stopped.";
    case "DISCONNECTED":
    case "INTERRUPTED":
      return "Paused because it is not known whether the last message went through.";
  }
  return `Paused: ${describeCode(reason)}`;
}

export function QueuePanel({ chatId, busy, tick, onSent, onQueue }: { chatId: string; busy: boolean; tick: number; onSent: (runId: string, input: string) => void; onQueue?: (q: ChatQueue) => void }) {
  const [queue, setQueue] = useState<ChatQueue | null>(null);
  const [error, setError] = useState("");
  const [working, setWorking] = useState(false);
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(null);

  // The parent hears every queue the server answered with; held in a ref so a new callback each render does not refetch.
  const tell = useRef(onQueue);
  tell.current = onQueue;
  const show = useCallback((q: ChatQueue) => {
    setQueue(q);
    tell.current?.(q);
  }, []);
  const load = useCallback(async () => {
    const r = await getQueue(chatId);
    if (r.ok) {
      show(r.value);
      setError("");
    } else setError(describeCode(r.code));
  }, [chatId, show]);

  useEffect(() => {
    setQueue(null);
    setEditing(null);
  }, [chatId]);
  useEffect(() => {
    void load();
  }, [load, tick, busy]);

  // Every change answers with the queue as stored; a refusal reloads it, so the screen never keeps a guess.
  const change = async (act: () => Promise<{ ok: true; value: ChatQueue } | { ok: false; code: string }>) => {
    setWorking(true);
    const r = await act();
    setWorking(false);
    if (r.ok) {
      show(r.value);
      setError("");
    } else {
      setError(describeCode(r.code));
      await load();
    }
  };

  const sendNext = async (head: QueuedMessage) => {
    setWorking(true);
    const r = await sendQueuedNext(chatId);
    setWorking(false);
    setError(r.ok ? "" : describeCode(r.code));
    if (r.ok) onSent(r.value.runId, head.input);
    await load();
  };

  if (!queue || (queue.items.length === 0 && queue.paused === "" && !error)) return null;
  const head = queue.items[0];
  return (
    <section className="queue" aria-label="Queued messages">
      <h2 className="who">Queued messages</h2>
      {queue.paused !== "" ? <p role="status">{describePause(queue.paused)}</p> : queue.items.length > 0 && <p className="label">Sent one at a time after each reply ends.</p>}
      <ol>
        {queue.items.map((it) => (
          <li key={it.id} aria-label={`Queued message ${it.position} of ${queue.items.length}`}>
            {editing?.id === it.id ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  const text = editing.text.trim();
                  if (text) void change(() => editQueued(chatId, it.id, text)).then(() => setEditing(null));
                }}
              >
                <label>
                  <span className="sr">Edit queued message {it.position}</span>
                  <textarea rows={2} value={editing.text} onChange={(e) => setEditing({ id: it.id, text: e.target.value })} />
                </label>
                <button type="submit" disabled={working || editing.text.trim() === ""}>
                  Save
                </button>{" "}
                <button type="button" className="quiet" onClick={() => setEditing(null)}>
                  Cancel edit
                </button>
              </form>
            ) : (
              <>
                <span className="label">{it.position}.</span> <span className="plain">{it.input}</span>
                {it.sending ? (
                  <span role="status"> Sending…</span>
                ) : (
                  <span className="actions">
                    {" "}
                    <button type="button" className="quiet" disabled={working} onClick={() => setEditing({ id: it.id, text: it.input })}>
                      Edit
                    </button>{" "}
                    <button type="button" className="quiet" disabled={working} onClick={() => void change(() => removeQueued(chatId, it.id))}>
                      Remove
                    </button>
                  </span>
                )}
              </>
            )}
          </li>
        ))}
      </ol>
      <div className="actions">
        <button type="button" className="quiet" disabled={working} onClick={() => void change(() => pauseQueue(chatId, queue.paused === ""))}>
          {queue.paused === "" ? "Pause queue" : "Resume queue"}
        </button>{" "}
        {head && (
          <button type="button" disabled={working || busy || head.sending} onClick={() => void sendNext(head)} aria-describedby={busy ? "queue-wait" : undefined}>
            Send next
          </button>
        )}
        {busy && head && (
          <span id="queue-wait" className="label">
            {" "}
            Available when the current reply ends.
          </span>
        )}
      </div>
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}

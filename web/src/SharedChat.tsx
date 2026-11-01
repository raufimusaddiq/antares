import { useEffect, useState } from "react";
import { Markdown } from "./Markdown";
import { MessageBody } from "./MessageBody";

interface Shared {
  title: string;
  sharedAt: string;
  turns: { role: "user" | "assistant"; text: string }[];
}

function parseShared(v: unknown): Shared | null {
  if (typeof v !== "object" || v === null) return null;
  const o = v as Record<string, unknown>;
  if (typeof o.title !== "string" || typeof o.sharedAt !== "string" || !Array.isArray(o.turns)) return null;
  const turns = o.turns.filter(
    (t): t is Shared["turns"][number] => typeof t === "object" && t !== null && (t.role === "user" || t.role === "assistant") && typeof t.text === "string",
  );
  return { title: o.title, sharedAt: o.sharedAt, turns };
}

/** The address of a shared chat, /s/<token>, or null. */
export const sharedToken = (path: string): string | null => /^\/s\/([A-Za-z0-9_-]{43})$/.exec(path)?.[1] ?? null;

/**
 * A chat someone shared by link (#250), read without signing in: the server answers it without looking at a session,
 * and the page shows only what the link holds: the title, the date and the turns' text.
 */
export function SharedChat({ token }: { token: string }) {
  const [state, setState] = useState<Shared | "loading" | "gone" | "busy" | "error">("loading");
  useEffect(() => {
    const ctl = new AbortController();
    fetch(`/api/shared/${token}`, { credentials: "same-origin", headers: { Accept: "application/json" }, signal: ctl.signal })
      .then(async (r) => (r.status === 404 ? "gone" : r.status === 429 ? "busy" : r.ok ? (parseShared(await r.json()) ?? "error") : "error"))
      .catch(() => (ctl.signal.aborted ? "loading" : "error"))
      .then((s) => !ctl.signal.aborted && setState(s));
    return () => ctl.abort();
  }, [token]);
  useEffect(() => {
    document.title = typeof state === "object" ? `${state.title || "Shared chat"} · Antares` : "Shared chat · Antares";
  }, [state]);

  if (state === "loading")
    return (
      <main className="shared" aria-busy="true">
        <p>Loading…</p>
      </main>
    );
  if (typeof state === "string") {
    const [head, body] = {
      gone: ["This link does not open", "The person who shared it may have stopped sharing, made a new link, or deleted the chat."],
      busy: ["Too many people are reading shared chats", "Wait a minute, then reload the page."],
      error: ["Could not load this chat", "Check your connection and reload the page."],
    }[state];
    return (
      <main className="shared">
        <h1>{head}</h1>
        <p>{body}</p>
      </main>
    );
  }
  return (
    <main className="shared">
      <header className="shared-head">
        <span className="label">Shared conversation</span>
        <h1>{state.title || "Antares chat"}</h1>
        <p className="label">A copy made on {state.sharedAt.slice(0, 10)}. Messages after that are not included; images and files were left out.</p>
      </header>
      {state.turns.map((t, i) => (
        <article key={i} className={`turn ${t.role}`}>
          <h2 className="who">{t.role === "user" ? "Question" : "Assistant"}</h2>
          {t.role === "user" ? <Markdown text={t.text} /> : <MessageBody text={t.text} streaming={false} />}
        </article>
      ))}
      <p className="shared-foot label">
        Shared from <a href="/">Antares</a>.
      </p>
    </main>
  );
}

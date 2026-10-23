import { useEffect, useRef, useState, type FormEvent } from "react";
import { describeCode, searchChats, type SearchPage, type SearchResult, type Segment } from "./chatApi";

// Chat search (UXP-003). Snippets arrive as segments of plain text; the matched span is
// wrapped in <mark> by React, so message content is only ever a text node: it is never
// parsed as markup, whatever it contains.

export function Snippet({ segments }: { segments: readonly Segment[] }) {
  return (
    <>
      {segments.map((s, i) => (s.hit ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}
    </>
  );
}

/** The same rule the server enforces: every word has 2+ letters (1 is enough in Han, Hiragana, Katakana, Hangul), at most 6 words. */
export function searchable(q: string): boolean {
  const words = q.split(/\s+/).filter(Boolean);
  if (words.length === 0 || words.length > 6) return false;
  return words.every((w) => [...w].length >= 2 || /^[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]$/u.test(w));
}

/** A timestamp as a short local date, or "" when it is not a date at all. */
export function when(iso: string): string {
  const d = new Date(iso);
  return iso === "" || Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

const who = (r: SearchResult) => (r.kind === "title" ? "Title" : r.role === "user" ? "You" : "Assistant");

export interface SearchState {
  query: string;
  page: SearchPage | null;
  /** An error code from the API vocabulary; the words come from describeCode. */
  error: string;
  loading: boolean;
  /** The box holds words the server would refuse (too short): nothing was sent. */
  tooShort?: boolean;
  /** The request that produced `page`: "More results" repeats this, never what the box says now. */
  params: { q: string; chatId?: string; role?: "user" | "assistant" } | null;
}

/** The results and the honest statements around them. Pure, so it renders to static markup in tests. */
export function SearchResults({ state, onOpen, onMore }: { state: SearchState; onOpen: (r: SearchResult) => void; onMore: () => void }) {
  const { page, error, loading, query } = state;
  if (error) {
    return (
      <p className="notice" role="alert">
        Search unavailable: {describeCode(error)}
      </p>
    );
  }
  if (state.tooShort) {
    return (
      <p className="label" role="status">
        Each search word needs at least 2 letters.
      </p>
    );
  }
  if (!page) return loading ? <p className="label">Searching…</p> : null;
  const { pending, partial, failed, chats } = page.index;
  const incomplete = !page.index.complete;
  return (
    <div className="search-results">
      {page.results.length === 0 ? (
        <p className="label" role="status">
          {incomplete ? `No matches yet for “${query}”.` : `No matches for “${query}”.`}
        </p>
      ) : (
        <ul aria-label="Search results">
          {page.results.map((r, i) => (
            <li key={`${r.chatId}-${r.turn ?? "title"}-${i}`}>
              <button type="button" className="hit" onClick={() => onOpen(r)}>
                <span className="hit-chat">{r.chatTitle || "New chat"}</span>
                <span className="hit-who">
                  {who(r)}
                  {when(r.createdAt) && (
                    <>
                      {" · "}
                      <time dateTime={r.createdAt}>{when(r.createdAt)}</time>
                    </>
                  )}
                </span>
                <span className="hit-text">
                  <Snippet segments={r.snippet} />
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {pending > 0 && (
        <p className="label" role="status">
          Still indexing {pending} of {chats} conversations. Results may be incomplete; search again in a moment.
        </p>
      )}
      {failed > 0 && (
        <p className="label" role="status">
          {failed} of {chats} conversations could not be read just now and are not searched. Try again later.
        </p>
      )}
      {partial > 0 && (
        <p className="label" role="status">
          {partial} of {chats} conversations are only partly indexed (they are too long). Text in the unindexed part cannot be found.
        </p>
      )}
      {page.nextCursor && (
        <button type="button" className="quiet" disabled={loading} onClick={onMore}>
          {loading ? "Loading…" : "More results"}
        </button>
      )}
    </div>
  );
}

/** The search box in the sidebar. `current` enables "this chat only". */
export function SearchPanel({ current, onOpen }: { current: string | null; onOpen: (r: SearchResult) => void }) {
  const [text, setText] = useState("");
  const [thisChat, setThisChat] = useState(false);
  const [role, setRole] = useState<"" | "user" | "assistant">("");
  const [state, setState] = useState<SearchState>({ query: "", page: null, error: "", loading: false, params: null });
  const seq = useRef(0); // only the newest request may write: an older answer arriving late is dropped

  // A new search reads the box; "More results" (cursor given) repeats the stored params, because the
  // cursor is a position in that query's results and means nothing for any other.
  const run = (cursor?: string) => {
    const params = cursor && state.params ? state.params : { q: text.trim(), chatId: thisChat && current ? current : undefined, role: role || undefined };
    const q = params.q;
    if (!searchable(q)) {
      seq.current++;
      setState({ query: q, page: null, error: "", loading: false, params: null, tooShort: q !== "" });
      return;
    }
    const mine = ++seq.current;
    setState((s) => ({ query: q, page: cursor ? s.page : null, error: "", loading: true, params }));
    void searchChats(q, { chatId: params.chatId, role: params.role, cursor }).then((r) => {
      if (mine !== seq.current) return;
      if (!r.ok) {
        setState({ query: q, page: null, error: r.code, loading: false, params: null });
        return;
      }
      setState((s) => ({
        query: q,
        error: "",
        loading: false,
        params,
        page: cursor && s.page ? { ...r.value, results: [...s.page.results, ...r.value.results] } : r.value,
      }));
    });
  };

  useEffect(() => () => void seq.current++, []);

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    run();
  };

  return (
    <section className="search" aria-label="Search conversations">
      <form role="search" onSubmit={onSubmit}>
        <label htmlFor="search-input" className="sr">
          Search conversations
        </label>
        <input id="search-input" type="search" value={text} maxLength={200} placeholder="Search conversations" onChange={(e) => setText(e.target.value)} />
        <div className="search-filters">
          <label>
            <span className="sr">Who wrote it</span>
            <select value={role} onChange={(e) => setRole(e.target.value as "" | "user" | "assistant")}>
              <option value="">Everyone</option>
              <option value="user">You</option>
              <option value="assistant">Assistant</option>
            </select>
          </label>
          <label>
            <input type="checkbox" checked={thisChat} disabled={!current} onChange={(e) => setThisChat(e.target.checked)} /> This chat only
          </label>
        </div>
      </form>
      <SearchResults state={state} onOpen={onOpen} onMore={() => run(state.page?.nextCursor)} />
    </section>
  );
}

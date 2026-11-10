import { useEffect, useRef, useState, type FormEvent, type MouseEvent } from "react";
import { describeAdminCode } from "./adminApi";
import { Forbidden, Loading, Problem, useLoad } from "./adminLoad";
import { MAX_REASON, getAdminPost, listAdminPosts, restorePost, takeDownPost, type AdminPost, type AdminPostView, type PostFilter, type PostState } from "./adminPostsApi";
import { adminPostPath } from "./route";
import { DocView } from "./blog/DocView";
import { publicImageUrl } from "./blog/Public";
import { mediaUrl } from "./blog/blogApi";

// The administrator's Posts screens (specs/004-admin-full-control): published posts, what a visitor could read, take down with a
// reason, restore. Drafts, later versions, checks and readers are never here: the server does not send them.

const day = (iso: string) => (iso ? iso.slice(0, 10) : "—");

const STATES: { id: PostState | ""; label: string }[] = [
  { id: "", label: "All" },
  { id: "visible", label: "Visible" },
  { id: "hidden_account", label: "Hidden (author's account)" },
  { id: "taken_down", label: "Taken down" },
];

const STATE_WORDS: Record<PostState, string> = { visible: "Visible", hidden_account: "Hidden · account", taken_down: "Taken down" };

/** The word carries the state; the class only adds colour. */
export function StateBadge({ state }: { state: PostState }) {
  return <span className={state === "visible" ? "badge ok" : state === "hidden_account" ? "badge hold" : "badge bad"}>{STATE_WORDS[state]}</span>;
}

function go(e: MouseEvent, onOpen: (path: string) => void, path: string) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  onOpen(path);
}

export function PostFilters({ filter, onFilter, search, onSearch }: { filter: PostState | ""; onFilter: (s: PostState | "") => void; search: string; onSearch: (q: string) => void }) {
  const [text, setText] = useState(search);
  return (
    <>
      <form
        className="search"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          onSearch(text.trim());
        }}
      >
        <label htmlFor="post-search" className="sr">
          Search by title or author
        </label>
        <input id="post-search" type="search" value={text} maxLength={80} placeholder="Search title or author" onChange={(e) => setText(e.target.value)} />
        <button type="submit" className="quiet">
          Search
        </button>
        {search && (
          <button
            type="button"
            className="quiet"
            onClick={() => {
              setText("");
              onSearch("");
            }}
          >
            Clear search
          </button>
        )}
      </form>
      <div className="filters" role="group" aria-label="Filter by state">
        {STATES.map((s) => (
          <button key={s.id || "all"} type="button" className={filter === s.id ? "quiet on" : "quiet"} aria-pressed={filter === s.id} onClick={() => onFilter(s.id)}>
            {s.label}
          </button>
        ))}
      </div>
    </>
  );
}

export function PostTable({ posts, onOpen }: { posts: AdminPost[]; onOpen: (path: string) => void }) {
  return (
    <div className="table-wrap">
      <table className="admin-table user-table" role="table">
        <thead role="rowgroup">
          <tr role="row">
            <th scope="col" role="columnheader">Post</th>
            <th scope="col" role="columnheader">Author</th>
            <th scope="col" role="columnheader">Published</th>
            <th scope="col" role="columnheader">State</th>
          </tr>
        </thead>
        <tbody role="rowgroup">
          {posts.map((p) => (
            <tr key={p.id} role="row">
              <th scope="row" role="rowheader">
                <a href={adminPostPath(p.id)} onClick={(e) => go(e, onOpen, adminPostPath(p.id))}>
                  {p.title || "Untitled"}
                </a>
                <span className="sub">{p.takedown ? `Taken down ${day(p.takedown.at)} · ${p.takedown.reason}` : p.summary}</span>
              </th>
              <td role="cell">
                <span className="cell-label">Author</span>
                {p.authorName || "—"}
              </td>
              <td role="cell" className="when">
                <span className="cell-label">Published</span>
                {day(p.publishedAt)}
              </td>
              <td role="cell">
                <span className="cell-label">State</span>
                <StateBadge state={p.state} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** One result set for one (state, search) pair; a new query starts from scratch. Later pages are appended behind "Show more". */
function PostResults({ filter, onOpen }: { filter: PostFilter; onOpen: (path: string) => void }) {
  const [state, reload] = useLoad((s) => listAdminPosts(filter, "", s), "first");
  const [more, setMore] = useState<AdminPost[]>([]);
  const [next, setNext] = useState<string | null>(null); // null: the first page's cursor is still the one in `state`
  const [busy, setBusy] = useState(false);
  const [moreError, setMoreError] = useState("");
  if (state.kind === "loading") return <Loading what="posts" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <Problem message={state.message} onRetry={reload} />;
  const cursor = next ?? state.value.next;
  const posts = [...state.value.items, ...more];
  const loadMore = () => {
    setBusy(true);
    setMoreError("");
    void listAdminPosts(filter, cursor).then((r) => {
      setBusy(false);
      if (!r.ok) return setMoreError(describeAdminCode(r.code, r.status));
      setMore((m) => [...m, ...r.value.items]);
      setNext(r.value.next);
    });
  };
  const searching = filter.q !== "" || filter.state !== "";
  return (
    <>
      {posts.length === 0 && (
        <div className="notice-card">
          <h2>{searching ? `No posts match${filter.q ? ` “${filter.q}”` : ""}.` : "Nothing is published yet."}</h2>
          <p>{searching ? "Search looks at the title, the summary and the author's name of published posts." : "When someone publishes a post it appears here and on the public blog."}</p>
        </div>
      )}
      {posts.length > 0 && <PostTable posts={posts} onOpen={onOpen} />}
      {posts.length > 0 && <p className="label">{cursor ? `${posts.length} posts loaded; more exist.` : `${posts.length} post${posts.length === 1 ? "" : "s"}.`}</p>}
      {moreError && <Problem message={moreError} onRetry={loadMore} />}
      {cursor && (
        <button type="button" className="quiet" disabled={busy} onClick={loadMore}>
          {busy ? "Loading…" : "Show more"}
        </button>
      )}
    </>
  );
}

export function AdminPostsScreen({ onOpen }: { onOpen: (path: string) => void }) {
  const [state, setState] = useState<PostState | "">("");
  const [q, setQ] = useState("");
  return (
    <section aria-labelledby="posts-h">
      <h1 id="posts-h">Posts</h1>
      <p className="label">Published posts only. Drafts, unpublished versions and who reads what are never shown here.</p>
      <PostFilters filter={state} onFilter={setState} search={q} onSearch={setQ} />
      <PostResults key={`${state}|${q}`} filter={{ state, q }} onOpen={onOpen} />
    </section>
  );
}

// ---- one post --------------------------------------------------------------------

export function ReasonForm({ busy, error, onCancel, onConfirm }: { busy: boolean; error: string; onCancel: () => void; onConfirm: (reason: string) => void }) {
  const [reason, setReason] = useState("");
  const [touched, setTouched] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  useEffect(() => form.current?.focus(), []);
  const empty = reason.trim() === "";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!empty) onConfirm(reason.trim());
  };
  return (
    <form ref={form} tabIndex={-1} className="confirm" onSubmit={submit} aria-label="Confirm: Take down">
      <h3>Take down this post</h3>
      <p>The post leaves the blog, search and its address straight away, and the author is told. Copies already saved elsewhere cannot be removed.</p>
      <label htmlFor="takedown-reason">Reason (required, shown to the author)</label>
      <textarea id="takedown-reason" value={reason} maxLength={MAX_REASON} rows={3} aria-invalid={touched && empty} aria-describedby={touched && empty ? "takedown-reason-err" : undefined} onChange={(e) => setReason(e.target.value)} />
      <p className="label">
        {reason.length} / {MAX_REASON}
      </p>
      {touched && empty && (
        <p id="takedown-reason-err" className="notice" role="alert">
          Write a reason. The author needs to know why.
        </p>
      )}
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
      <button type="submit" disabled={busy}>
        {busy ? "Working…" : "Confirm take down"}
      </button>
      <button type="button" className="quiet" disabled={busy} onClick={onCancel}>
        Cancel
      </button>
    </form>
  );
}

export function RestoreForm({ busy, error, onCancel, onConfirm }: { busy: boolean; error: string; onCancel: () => void; onConfirm: () => void }) {
  const form = useRef<HTMLFormElement>(null);
  useEffect(() => form.current?.focus(), []);
  return (
    <form
      ref={form}
      tabIndex={-1}
      className="confirm"
      aria-label="Confirm: Restore"
      onSubmit={(e) => {
        e.preventDefault();
        onConfirm();
      }}
    >
      <h3>Restore this post</h3>
      <p>The post goes live again exactly as it was, at the same address, in the blog and in search. The author sees that it was restored.</p>
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
      <button type="submit" disabled={busy}>
        {busy ? "Working…" : "Confirm restore"}
      </button>
      <button type="button" className="quiet" disabled={busy} onClick={onCancel}>
        Cancel
      </button>
    </form>
  );
}

export function PostView({ post, onChanged }: { post: AdminPostView; onChanged: (outcome: { note?: string }) => void }) {
  const [pick, setPick] = useState<"takedown" | "restore" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const cover = mediaUrl(post.coverUrl);
  const run = async (what: "takedown" | "restore", reason = "") => {
    setBusy(true);
    setError("");
    const r = what === "takedown" ? await takeDownPost(post.id, reason) : await restorePost(post.id);
    setBusy(false);
    if (!r.ok) return setError(describeAdminCode(r.code, r.status));
    setPick(null);
    onChanged(what === "restore" && "note" in r.value ? { note: r.value.note } : {});
  };
  return (
    <article className="admin-post">
      <h1>{post.title || "Untitled"}</h1>
      <p className="facts-row">
        <strong>{post.authorName || "—"}</strong>
        <span className="label">Published {day(post.publishedAt)}</span>
        <StateBadge state={post.state} />
        {post.state === "visible" && post.slug && (
          <a href={`/b/${post.slug}`} target="_blank" rel="noopener noreferrer">
            /b/{post.slug}
          </a>
        )}
        {post.state === "hidden_account" && <span className="label">Not public while the account is not approved</span>}
        {post.state === "taken_down" && <span className="label">Not public</span>}
      </p>
      {post.takedown && (
        <div className="notice-card taken" role="status">
          <h2>
            Taken down on {day(post.takedown.at)}
            {post.takedown.byName ? ` by ${post.takedown.byName}` : ""}
          </h2>
          <p>Reason shown to the author: {post.takedown.reason}</p>
        </div>
      )}
      <p className="label">
        {post.doc ? "This is the published version, as visitors see it. Drafts and later edits are never shown here." : "The author has unpublished this post since, so there is no published version to show."}
      </p>
      {post.doc && (
        <div className="admin-post-content">
          {cover && <img className="blog-cover" src={cover} alt="" />}
          <DocView doc={post.doc} blocks={post.blocks} imageUrl={publicImageUrl(post.slug)} />
        </div>
      )}
      <div className="actions">
        {post.state === "taken_down" ? (
          <button type="button" disabled={busy} onClick={() => setPick("restore")}>
            Restore
          </button>
        ) : (
          <button type="button" className="quiet" disabled={busy} onClick={() => setPick("takedown")}>
            Take down
          </button>
        )}
        {!pick && <span className="label">{post.state === "taken_down" ? "The post returns exactly as it was, at the same address." : "Visitors stop seeing it straight away (a browser may keep a copy for up to a minute). The author is told and can see your reason."}</span>}
      </div>
      {pick === "takedown" && <ReasonForm busy={busy} error={error} onCancel={() => setPick(null)} onConfirm={(r) => void run("takedown", r)} />}
      {pick === "restore" && <RestoreForm busy={busy} error={error} onCancel={() => setPick(null)} onConfirm={() => void run("restore")} />}
      {!pick && error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
    </article>
  );
}

export function AdminPostScreen({ id, onBack }: { id: string; onBack: () => void }) {
  const [state, reload] = useLoad((s) => getAdminPost(id, s), `post-${id}`);
  const [note, setNote] = useState("");
  const back = (
    <a href="/admin/posts" className="back" onClick={(e) => go(e, onBack, "/admin/posts")}>
      ← Posts
    </a>
  );
  if (state.kind === "loading") return <Loading what="the post" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <section>{back}<Problem message={state.message} onRetry={reload} /></section>;
  if (note) {
    return (
      <section>
        {back}
        <div className="notice-card ok" role="status">
          <h2>Restored.</h2>
          <p>{note}</p>
        </div>
      </section>
    );
  }
  return (
    <section>
      {back}
      <PostView post={state.value} onChanged={(o) => (o.note ? setNote(o.note) : reload())} />
    </section>
  );
}

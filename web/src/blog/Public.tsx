import { useEffect, useState } from "react";
import { getCapabilities, getPublic, listMyPublishedSlugs, listPublic, mediaUrl } from "./blogApi";
import { ReaderProvider, isActive, useReader } from "./readerSession";
import { LOGIN_URL } from "../api";
import { AntaresMark } from "../brand/AntaresMark";
import { formatDay } from "./dates";
import { DocView } from "./DocView";
import type { PublicPost, PublicSummary } from "./types";
import { ShareMenu } from "./share/ShareMenu";
import { StoryDialog } from "./share/StoryDialog";
import { highlights, summaryText } from "./share/postContent";
import { canonicalUrl } from "./share/shareLinks";

// The signed-out blog (the Blog public frames). No session is loaded and nothing of the app mounts: the server
// answers these routes without looking at a session, and every field shown is one the public API returns.

/** /b is the index, /b/<slug> a post; anything else is not a blog address. */
export function publicBlogRoute(path: string): { kind: "index" } | { kind: "post"; slug: string } | null {
  const p = path.replace(/\/+$/, "") || "/";
  if (p === "/b") return { kind: "index" };
  const m = /^\/b\/([a-z0-9][a-z0-9-]{1,78}[a-z0-9])$/.exec(p);
  return m ? { kind: "post", slug: m[1] } : null;
}

const initials = (n: string) => n.split(/\s+/).map((w) => w[0] ?? "").join("").slice(0, 2).toUpperCase();

function Frame({ children, title, share }: { children: React.ReactNode; title: string; share?: React.ReactNode }) {
  useEffect(() => {
    document.title = title;
  }, [title]);
  const reader = useReader();
  const [writing, setWriting] = useState(false);
  const active = isActive(reader);
  useEffect(() => {
    if (!active) return setWriting(false);
    const ctl = new AbortController();
    void getCapabilities(ctl.signal).then((r) => !ctl.signal.aborted && setWriting(r.ok && r.value.writing === true));
    return () => ctl.abort();
  }, [active]);
  return (
    <div className="blog-public">
      <header className="blog-public-head">
        <a className="blog-brand" href="/b">
          <AntaresMark />
          <span>Antares</span>
          <span className="label">Blog</span>
        </a>
        <div className="blog-public-actions">
          {share}
          {writing && (
            <a className="btn plain" href="/blog" data-icon="edit">
              <span>My<span className="wide-only"> blog</span> posts</span>
            </a>
          )}
          {reader.kind === "anonymous" && (
            <a className="btn" href={LOGIN_URL} aria-label="Sign in with Google">
              <span>
                Sign in<span className="wide-only"> with Google</span>
              </span>
            </a>
          )}
          {reader.kind === "account" && (
            <a className="btn plain" href="/" aria-label="Back to Antares">
              <span>
                Back<span className="wide-only"> to Antares</span>
              </span>
            </a>
          )}
        </div>
      </header>
      {children}
      <footer className="blog-public-foot label">Written by Antares users. Blocks marked AI-added were proposed by an assistant and approved by the author.</footer>
    </div>
  );
}

const PAGE = 20; // the server's page size: a full page means there may be more

export function PublicIndex() {
  return (
    <ReaderProvider>
      <IndexBody />
    </ReaderProvider>
  );
}

function IndexBody() {
  const reader = useReader();
  const [text, setText] = useState(""); // what is typed
  const [q, setQ] = useState(""); // what is searched, a moment after typing stops
  const [posts, setPosts] = useState<PublicSummary[]>([]);
  const [page, setPage] = useState(1);
  const [state, setState] = useState<"loading" | "ready" | "error" | "more" | "more-error">("loading");
  const [tick, setTick] = useState(0);
  const [mine, setMine] = useState<Set<string>>(new Set());
  // The cards ease in on the first load and when more arrive, never while someone is typing a search.
  const [enter, setEnter] = useState(true);
  const [full, setFull] = useState(false);
  const active = isActive(reader);

  useEffect(() => {
    const t = window.setTimeout(() => setQ(text.trim().replace(/\s+/g, " ")), 250);
    return () => window.clearTimeout(t);
  }, [text]);
  useEffect(() => {
    if (!active) return;
    const ctl = new AbortController();
    void listMyPublishedSlugs(ctl.signal).then((s) => !ctl.signal.aborted && setMine(s));
    return () => ctl.abort();
  }, [active]);
  // A new search (or a retry of the first page) starts again from page 1.
  useEffect(() => {
    const ctl = new AbortController();
    setState("loading");
    void listPublic({ q }, ctl.signal).then((r) => {
      if (ctl.signal.aborted) return;
      if (!r.ok) return setState("error");
      setPosts(r.value);
      setPage(1);
      setFull(r.value.length >= PAGE);
      setEnter(q === "");
      setState("ready");
    });
    return () => ctl.abort();
  }, [q, tick]);

  const more = () => {
    setState("more");
    void listPublic({ q, page: page + 1 }).then((r) => {
      if (!r.ok) return setState("more-error");
      // A post published while reading shifts the next page by one: a slug already shown is never shown twice.
      setPosts((cur) => {
        const seen = new Set(cur.map((p) => p.slug));
        return [...cur, ...r.value.filter((p) => !seen.has(p.slug))];
      });
      setPage(page + 1);
      setFull(r.value.length >= PAGE);
      setEnter(true);
      setState("ready");
    });
  };

  const first = state === "loading" || state === "error";
  const shown = first && state === "error" ? [] : posts;
  return (
    <Frame title="Blog · Antares">
      <main className="blog-index">
        <h1>Writing from Antares users</h1>
        <p className="blog-lede">Posts by people who use the assistant, some with live tables and charts.</p>
        <div className="blog-search">
          <input type="search" value={text} onChange={(e) => setText(e.target.value)} placeholder="Search posts" aria-label="Search posts" maxLength={80} autoComplete="off" />
          {text !== "" && (
            <button type="button" className="quiet" onClick={() => { setText(""); setQ(""); }}>
              Clear
            </button>
          )}
        </div>
        <p className="label blog-count" role="status" aria-live="polite">
          {state === "ready" || state === "more" || state === "more-error" ? (posts.length === 0 ? (q ? "No posts match" : "") : `${posts.length} post${posts.length === 1 ? "" : "s"}${full ? "+" : ""}`) : ""}
        </p>
        {state === "loading" && <p aria-busy="true">Loading posts…</p>}
        {state === "error" && (
          <div role="alert" className="blog-empty">
            <h2>We could not load the posts</h2>
            <p>The blog did not answer. Nothing is lost; try again in a moment.</p>
            <button type="button" className="btn" onClick={() => setTick(tick + 1)}>Try again</button>
          </div>
        )}
        {state !== "loading" && state !== "error" && posts.length === 0 && (
          q ? (
            <div className="blog-empty"><h2>No posts match</h2><p>Try another word, or clear the search to see every post.</p></div>
          ) : (
            <div className="blog-empty"><h2>Nothing published yet</h2><p>When someone publishes a post it appears here.</p></div>
          )
        )}
        {shown.length > 0 && (
          <ul className="blog-cards" data-enter={enter ? "" : undefined}>
            {shown.map((p, i) => (
              <li key={p.slug} className="blog-card" style={{ "--i": Math.min(i % PAGE, 8) } as React.CSSProperties}>
                <a href={`/b/${p.slug}`}>
                  {mediaUrl(p.coverUrl) && <img src={mediaUrl(p.coverUrl)} alt="" loading="lazy" />}
                  <span className="label">
                    {formatDay(p.publishedAt)}
                    {mine.has(p.slug) && <span className="chip chip-yours">Yours</span>}
                  </span>
                  <h2>{p.title}</h2>
                  <p>{p.summary}</p>
                  <span className="blog-by"><span className="avatar" aria-hidden="true">{initials(p.authorName)}</span> {p.authorName}</span>
                </a>
              </li>
            ))}
          </ul>
        )}
        {state === "more-error" && (
          <p role="alert" className="blog-more-error">We could not load more posts.</p>
        )}
        {(state === "ready" || state === "more" || state === "more-error") && full && (
          <div className="blog-pager">
            <button type="button" className="quiet" onClick={more} disabled={state === "more"} aria-busy={state === "more"}>
              {state === "more" ? "Loading more…" : state === "more-error" ? "Try again" : "Show more"}
            </button>
          </div>
        )}
      </main>
    </Frame>
  );
}

/** An image the author uploaded is stored as the author-only address; a reader gets the published post's own copy. */
export function publicImageUrl(slug: string): (src: string) => string {
  return (src) => {
    const m = /^\/api\/blog\/posts\/[A-Za-z0-9_-]+\/assets\/([A-Za-z0-9_-]+)$/.exec(src);
    return m ? `/api/public/blog/posts/${encodeURIComponent(slug)}/assets/${m[1]}` : src;
  };
}

export function PublicPostPage({ slug }: { slug: string }) {
  return (
    <ReaderProvider>
      <PostBody slug={slug} />
    </ReaderProvider>
  );
}

function PostBody({ slug }: { slug: string }) {
  const [p, setP] = useState<PublicPost | "loading" | "gone" | "error">("loading");
  useEffect(() => {
    const ctl = new AbortController();
    setP("loading");
    void getPublic(slug, ctl.signal).then((r) => !ctl.signal.aborted && setP(r.ok ? r.value : r.status === 404 ? "gone" : "error"));
    return () => ctl.abort();
  }, [slug]);
  const title = typeof p === "object" ? `${p.title} · Antares Blog` : "Blog · Antares";
  const [story, setStory] = useState(false);
  const post = typeof p === "object" ? p : null;
  return (
    <Frame title={title} share={post ? <ShareMenu title={post.title} slug={slug} onStory={() => setStory(true)} /> : undefined}>
      {p === "loading" && <main aria-busy="true"><p>Loading…</p></main>}
      {p === "gone" && (
        <main className="blog-empty">
          <p className="label">404</p>
          <h1>This post is not available</h1>
          <p>It may have been unpublished or never existed.</p>
          <a className="btn" href="/b">See all posts</a>
        </main>
      )}
      {p === "error" && (
        <main className="blog-empty" role="alert">
          <h1>We could not load this post</h1>
          <p>Check your connection and reload the page.</p>
        </main>
      )}
      {typeof p === "object" && (
        <article className="blog-article">
          <a className="label" href="/b">Blog</a>
          <h1>{p.title}</h1>
          {p.summary && <p className="blog-lede">{p.summary}</p>}
          <p className="blog-byline">
            <span className="avatar" aria-hidden="true">{initials(p.authorName)}</span> <strong>{p.authorName}</strong>
            <span className="label"> · {formatDay(p.publishedAt)}</span>
            {p.blocks.length > 0 && <span className="chip chip-info">Interactive</span>}
          </p>
          {mediaUrl(p.coverUrl) && <img className="blog-cover" src={mediaUrl(p.coverUrl)} alt="" />}
          <DocView doc={p.doc} blocks={p.blocks} imageUrl={publicImageUrl(slug)} />
          <p className="blog-more-posts">
            <a className="btn plain" href="/b" data-icon="arrow-left">More posts</a>
          </p>
        </article>
      )}
      {post && story && (
        <StoryDialog
          slug={slug}
          url={canonicalUrl(window.location.origin, slug)}
          onClose={() => setStory(false)}
          data={{
            title: post.title,
            summary: summaryText(post.summary, post.doc),
            highlights: highlights(post.doc),
            authorName: post.authorName,
            coverUrl: mediaUrl(post.coverUrl),
            date: new Date(post.publishedAt),
            address: `${window.location.host}/b/${slug}`,
          }}
        />
      )}
    </Frame>
  );
}

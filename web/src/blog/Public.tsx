import { useEffect, useState } from "react";
import { getPublic, listPublic, mediaUrl } from "./blogApi";
import { LOGIN_URL } from "../api";
import { AntaresMark } from "../brand/AntaresMark";
import { formatDay } from "./dates";
import { DocView } from "./DocView";
import type { PublicPost, PublicSummary } from "./types";

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

function Frame({ children, title }: { children: React.ReactNode; title: string }) {
  useEffect(() => {
    document.title = title;
  }, [title]);
  return (
    <div className="blog-public">
      <header className="blog-public-head">
        <a className="blog-brand" href="/b">
          <AntaresMark />
          <span>Antares</span>
          <span className="label">Blog</span>
        </a>
        <a className="btn" href={LOGIN_URL}>Sign in with Google</a>
      </header>
      {children}
      <footer className="blog-public-foot label">Written by Antares users. Blocks marked AI-added were proposed by an assistant and approved by the author.</footer>
    </div>
  );
}

export function PublicIndex() {
  const [s, setS] = useState<PublicSummary[] | "loading" | "error">("loading");
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const ctl = new AbortController();
    setS("loading");
    void listPublic(ctl.signal).then((r) => !ctl.signal.aborted && setS(r.ok ? r.value : "error"));
    return () => ctl.abort();
  }, [tick]);
  return (
    <Frame title="Blog · Antares">
      <main className="blog-index">
        <h1>Writing from Antares users</h1>
        {s === "loading" && <p aria-busy="true">Loading posts…</p>}
        {s === "error" && (
          <div role="alert" className="blog-empty">
            <h2>We could not load the posts</h2>
            <p>The blog did not answer. Nothing is lost; try again in a moment.</p>
            <button type="button" className="btn" onClick={() => setTick(tick + 1)}>Try again</button>
          </div>
        )}
        {Array.isArray(s) && s.length === 0 && (
          <div className="blog-empty"><h2>Nothing published yet</h2><p>When someone publishes a post it appears here.</p></div>
        )}
        {Array.isArray(s) && s.length > 0 && (
          <ul className="blog-cards">
            {s.map((p) => (
              <li key={p.slug} className="blog-card">
                <a href={`/b/${p.slug}`}>
                  {mediaUrl(p.coverUrl) && <img src={mediaUrl(p.coverUrl)} alt="" loading="lazy" />}
                  <span className="label">{formatDay(p.publishedAt)}</span>
                  <h2>{p.title}</h2>
                  <p>{p.summary}</p>
                  <span className="blog-by"><span className="avatar" aria-hidden="true">{initials(p.authorName)}</span> {p.authorName}</span>
                </a>
              </li>
            ))}
          </ul>
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
  const [p, setP] = useState<PublicPost | "loading" | "gone" | "error">("loading");
  useEffect(() => {
    const ctl = new AbortController();
    setP("loading");
    void getPublic(slug, ctl.signal).then((r) => !ctl.signal.aborted && setP(r.ok ? r.value : r.status === 404 ? "gone" : "error"));
    return () => ctl.abort();
  }, [slug]);
  const title = typeof p === "object" ? `${p.title} · Antares Blog` : "Blog · Antares";
  return (
    <Frame title={title}>
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
        </article>
      )}
    </Frame>
  );
}

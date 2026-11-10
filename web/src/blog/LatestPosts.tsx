import { useEffect, useState } from "react";
import { listPublic } from "./blogApi";
import { formatDay } from "./dates";
import type { PublicSummary } from "./types";

/**
 * The three latest published posts, as cards, for the sign-in and waiting screens. It is an extra: it appears after the page
 * and only when there is something to show, so it never delays or blocks signing in. Empty, failed, slow or rate limited all
 * look the same: it is not there.
 */
export function LatestPosts() {
  const [posts, setPosts] = useState<PublicSummary[]>([]);
  useEffect(() => {
    const ctl = new AbortController();
    void listPublic({ limit: 3 }, ctl.signal).then((r) => !ctl.signal.aborted && r.ok && setPosts(r.value.slice(0, 3)));
    return () => ctl.abort();
  }, []);
  if (posts.length === 0) return null;
  return (
    <section className="latest-posts" aria-labelledby="latest-posts-h">
      <h2 id="latest-posts-h" className="label">Latest posts</h2>
      <ul>
        {posts.map((p) => (
          <li key={p.slug}>
            <a href={`/b/${p.slug}`}>
              <span className="latest-title">{p.title}</span>
              <span className="label">{p.authorName} · {formatDay(p.publishedAt)}</span>
            </a>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** The one button every screen has for the blog: a plain link to the public list. */
export function ReadTheBlog() {
  return (
    <a className="btn read-blog" href="/b" data-icon="book">
      Read the blog
    </a>
  );
}

import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { describeBlogError, getCapabilities, getPost } from "./blogApi";
import { loadLocalCopy } from "./autosave";
import { PostList } from "./PostList";
import { PostSettings } from "./PostSettings";
import { PublishFlow } from "./PublishFlow";
import type { Capabilities, Post } from "./types";

// The signed-in author area: My posts, the editor, post settings and the publish flow. The editor is loaded on demand.
const Editor = lazy(() => import("./Editor"));

export type BlogRoute = { kind: "list" } | { kind: "edit"; id: string } | { kind: "publish"; id: string };

export const isBlogPath = (p: string): boolean => p === "/blog" || p.startsWith("/blog/");

const ID = "[A-Za-z0-9_-]{6,64}";
export function parseBlogPath(p: string): BlogRoute {
  const path = p.replace(/\/+$/, "") || "/";
  const m = new RegExp(`^/blog/(${ID})(/publish)?$`).exec(path);
  if (m) return m[2] ? { kind: "publish", id: m[1] } : { kind: "edit", id: m[1] };
  return { kind: "list" };
}

export const editPath = (id: string) => `/blog/${id}`;
export const publishPath = (id: string) => `/blog/${id}/publish`;

export function BlogApp({ email, route, onNavigate, onChats }: { email: string; route: BlogRoute; onNavigate: (to: string) => void; onChats: () => void }) {
  const [caps, setCaps] = useState<Capabilities | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    void getCapabilities(ctl.signal).then((r) => !ctl.signal.aborted && setCaps(r.ok ? r.value : { writing: false, check: false, webSearch: false }));
    return () => ctl.abort();
  }, []);

  if (caps === null) return <main className="blog-flow" aria-busy="true"><p>Loading…</p></main>;
  if (!caps.writing)
    return (
      <main className="blog-flow" role="status">
        <h1>Blog posts</h1>
        <p>Writing blog posts is not available right now.</p>
        <button type="button" className="btn" onClick={onChats}>Back to chats</button>
      </main>
    );
  if (route.kind === "list") return <PostList onOpen={(id) => onNavigate(editPath(id))} onChats={onChats} />;
  return <PostScreen key={route.id} id={route.id} email={email} publishing={route.kind === "publish"} caps={caps} onNavigate={onNavigate} />;
}

function PostScreen({ id, email, publishing, caps, onNavigate }: { id: string; email: string; publishing: boolean; caps: Capabilities | null; onNavigate: (to: string) => void }) {
  const [post, setPost] = useState<Post | "loading" | string>("loading");
  const [settings, setSettings] = useState(false);
  // Without an open editor, "saved" means this browser holds nothing the server has not confirmed.
  const flush = useRef<() => Promise<boolean>>(() => Promise.resolve(loadLocalCopy(email, id) === null));

  useEffect(() => {
    const ctl = new AbortController();
    void getPost(id, ctl.signal).then((r) => !ctl.signal.aborted && setPost(r.ok ? r.value : describeBlogError(r, "open this post")));
    return () => ctl.abort();
  }, [id]);

  if (post === "loading")
    return <main className="blog-flow" aria-busy="true"><p>Loading…</p></main>;
  if (typeof post === "string")
    return (
      <main className="blog-flow" role="alert">
        <p>{post}</p>
        <button type="button" className="btn" onClick={() => onNavigate("/blog")}>My posts</button>
      </main>
    );

  if (publishing)
    return (
      <PublishFlow
        post={post}
        caps={caps}
        flush={() => flush.current()}
        onPost={setPost}
        onBackToEditing={() => onNavigate(editPath(id))}
        onMyPosts={() => onNavigate("/blog")}
      />
    );

  return (
    <Suspense fallback={<main className="blog-flow" aria-busy="true"><p>Loading the editor…</p></main>}>
      <Editor
        post={post}
        email={email}
        onPost={(p) => setPost((cur) => (typeof cur === "object" ? { ...cur, latestVersion: p.latestVersion, status: p.status, updatedAt: p.updatedAt } : p))}
        onSettings={() => setSettings(true)}
        onPublish={(f) => {
          flush.current = f;
          void f().then((saved) => saved && onNavigate(publishPath(id))); // a failed save stays in the editor, where its state shows
        }}
        onBack={() => onNavigate("/blog")}
      />
      {settings && <PostSettings post={post} onSaved={setPost} onClose={() => setSettings(false)} />}
    </Suspense>
  );
}

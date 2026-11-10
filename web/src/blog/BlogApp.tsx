import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { describeBlogError, getPost } from "./blogApi";
import { loadLocalCopy } from "./autosave";
import { PostList } from "./PostList";
import { PostSettings } from "./PostSettings";
import type { DraftBridge } from "./draftBridge";
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

/** `caps` is what the server said about the blog (null until it has answered); the app asks once and passes it down. */
export function BlogApp({ caps, email, name, route, onNavigate, onChats }: { caps: Capabilities | null; email: string; name?: string; route: BlogRoute; onNavigate: (to: string) => void; onChats: () => void }) {
  if (caps === null) return <main className="blog-flow" aria-busy="true"><p>Loading…</p></main>;
  if (!caps.writing)
    return (
      <main className="blog-flow" role="status">
        <h1>Blog posts</h1>
        <p>Writing blog posts is not available right now.</p>
        <button type="button" className="btn" onClick={onChats}>Back to chats</button>
      </main>
    );
  if (route.kind === "list") return <PostList email={email} onOpen={(id) => onNavigate(editPath(id))} onChats={onChats} />;
  return <PostScreen key={route.id} id={route.id} email={email} name={name} publishing={route.kind === "publish"} caps={caps} onNavigate={onNavigate} />;
}

function PostScreen({ id, email, name, publishing, caps, onNavigate }: { id: string; email: string; name?: string; publishing: boolean; caps: Capabilities | null; onNavigate: (to: string) => void }) {
  const [post, setPost] = useState<Post | "loading" | string>("loading");
  const [settings, setSettings] = useState(false);
  const [bridge, setBridge] = useState<DraftBridge | null>(null);
  // Without an open editor, "saved" means this browser holds nothing the server has not confirmed.
  const flush = useRef<() => Promise<boolean>>(() => Promise.resolve(loadLocalCopy(email, id) === null));
  // The editor is mounted behind Publish and stays through it; when Publish closes it is opened again on the latest post this
  // screen holds (what Publish saved and confirmed), not on what the editor was first given, and focus goes back to its Publish
  // button, since the button that had focus before the dialog is the old editor's and is gone.
  const [editorKey, setEditorKey] = useState(0);
  const wasPublishing = useRef(false);
  useEffect(() => {
    if (wasPublishing.current && !publishing) setEditorKey((k) => k + 1);
    wasPublishing.current = publishing;
  }, [publishing]);
  useEffect(() => {
    if (editorKey > 0) document.querySelector<HTMLElement>("[data-publish]")?.focus();
  }, [editorKey]);

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

  // The editor stays where it is, mounted behind Publish (the design's dialog sits over it): inert while Publish is open, so
  // nothing in it can be reached or read out, and its saved state and flush are the ones Publish asks for.
  return (
    <>
      <Suspense fallback={<main className="blog-flow" aria-busy="true"><p>Loading the editor…</p></main>}>
        <div className="blog-behind" inert={publishing} aria-hidden={publishing || undefined}>
          <Editor
            key={editorKey}
            post={post}
            email={email}
            onPost={(p) => setPost((cur) => (typeof cur === "object" ? { ...cur, title: p.title, summary: p.summary, slug: p.slug, coverUrl: p.coverUrl, doc: p.doc, check: p.check, latestVersion: p.latestVersion, status: p.status, updatedAt: p.updatedAt } : p))}
            onSettings={() => setSettings(true)}
            onPublish={(f) => {
              flush.current = f;
              void f().then((saved) => saved && onNavigate(publishPath(id))); // a failed save stays in the editor, where its state shows
            }}
            onBack={() => onNavigate("/blog")}
            onBridge={setBridge}
            publishing={publishing}
            canCheck={caps?.check === true}
          />
        </div>
        {settings && !publishing && <PostSettings post={post} onSaved={setPost} onClose={() => setSettings(false)} authorName={name} />}
      </Suspense>
      {publishing && (
        <PublishFlow
          post={post}
          caps={caps}
          flush={() => flush.current()}
          bridge={bridge}
          onPost={setPost}
          onBackToEditing={() => onNavigate(editPath(id))}
          onMyPosts={() => onNavigate("/blog")}
        />
      )}
    </>
  );
}

import { useCallback, useEffect, useRef, useState } from "react";
import { createPost, deletePost, describeBlogError, duplicatePost, listPosts, unpublish } from "./blogApi";
import type { BlogResult } from "./blogApi";
import { BlogShell } from "./BlogShell";
import { RowMenu, type RowAction } from "./RowMenu";
import { useDialog } from "./useDialog";
import { formatDay } from "./dates";
import type { PostStatus, PostSummary } from "./types";

export const STATUS_WORDS: Record<PostStatus, string> = {
  draft: "Draft",
  checking: "Checking",
  ready: "Ready to publish",
  published: "Published",
  unpublished: "Unpublished",
};

type Load = { k: "loading" } | { k: "error"; why: string } | { k: "ok"; posts: PostSummary[] };


export function PostList({ email, onOpen, onChats }: { email: string; onOpen: (id: string) => void; onChats: () => void }) {
  const [load, setLoad] = useState<Load>({ k: "loading" });
  const [confirm, setConfirm] = useState<PostSummary | null>(null);
  const [note, setNote] = useState("");
  // Where focus goes once a deleted post is gone: the row that took its place, else New post (the row it came from is unmounted).
  const focusAfterDelete = useRef<number | null>(null);
  const newPost = useRef<HTMLButtonElement>(null);

  const inflight = useRef<AbortController | null>(null);
  const refresh = useCallback(() => {
    inflight.current?.abort();
    const ctl = new AbortController();
    inflight.current = ctl;
    setLoad((l) => (l.k === "ok" ? l : { k: "loading" })); // a reload keeps the list on screen, so focus stays where it was
    void listPosts(ctl.signal).then((r) => {
      if (ctl.signal.aborted) return;
      setLoad(r.ok ? { k: "ok", posts: r.value } : { k: "error", why: describeBlogError(r, "load your posts") });
    });
  }, []);
  useEffect(() => {
    refresh();
    return () => inflight.current?.abort();
  }, [refresh]);

  const act = async (p: Promise<BlogResult<unknown>>, what: string) => {
    setNote("");
    const r = await p;
    if (r.ok) return refresh();
    focusAfterDelete.current = null;
    setNote(describeBlogError(r, what));
  };

  useEffect(() => {
    if (focusAfterDelete.current === null || load.k !== "ok") return;
    const at = focusAfterDelete.current;
    focusAfterDelete.current = null;
    const more = document.querySelectorAll<HTMLElement>(".blog-list .blog-more");
    (more.length > 0 ? more[Math.min(at, more.length - 1)] : newPost.current)?.focus();
  }, [load]);

  const creating = useRef(false);
  const create = async () => {
    if (creating.current) return; // a double click makes one draft
    creating.current = true;
    const r = await createPost().finally(() => (creating.current = false));
    if (r.ok) onOpen(r.value.id);
    else setNote(describeBlogError(r, "start a new post"));
  };

  const actionsOf = (p: PostSummary): RowAction[] => [
    { label: "Open", run: () => onOpen(p.id) },
    ...(p.status === "published" && p.slug ? [{ label: "View post", href: `/b/${p.slug}` }] : []),
    { label: "Duplicate", run: () => void act(duplicatePost(p.id), "duplicate") },
    ...(p.status === "published" ? [{ label: "Unpublish", run: () => void act(unpublish(p.id), "unpublish") }] : []),
    { label: "Delete", danger: true, run: () => setConfirm(p) },
  ];

  return (
    <BlogShell email={email} onNewPost={() => void create()} onChats={onChats}>
    <main className="blog-list">
      <header className="blog-bar">
        <div>
          <h1>My posts</h1>
          <p className="label blog-bar-note">Only you can see drafts. Published posts are public.</p>
        </div>
        <button ref={newPost} type="button" className="btn" onClick={() => void create()}>
          New post
        </button>
      </header>
      {note && <p className="blog-error" role="alert">{note}</p>}
      {load.k === "loading" && <p aria-busy="true">Loading your posts…</p>}
      {load.k === "error" && (
        <div role="alert">
          <p>{load.why}</p>
          <button type="button" className="btn" onClick={refresh}>
            Try again
          </button>
        </div>
      )}
      {load.k === "ok" && load.posts.length === 0 && (
        <div className="blog-empty">
          <h2>No posts yet</h2>
          <p>Write a post. When you publish, your assistant can check it and propose interactive blocks with sourced data.</p>
          <button type="button" className="btn" onClick={() => void create()}>
            Write your first post
          </button>
        </div>
      )}
      {load.k === "ok" && load.posts.length > 0 && (
        <ul className="blog-rows">
          {load.posts.map((p) => (
            <li key={p.id} className="blog-row">
              <span className="blog-row-text">
                <span className="blog-row-title">{p.title || "Untitled"}</span>
                <span className="label">
                  Edited {formatDay(p.updatedAt, false)}
                  {p.status === "published" && p.slug ? ` · /b/${p.slug}` : ""}
                </span>
              </span>
              <div className="blog-row-end">
                <span className={`chip chip-${p.status}`}>{STATUS_WORDS[p.status]}</span>
                {p.interactive && <span className="chip chip-ai">Interactive</span>}
                <button type="button" className="quiet" aria-label={`Open ${p.title || "Untitled"}`} onClick={() => onOpen(p.id)}>
                  Open
                </button>
                <RowMenu label={`More actions for ${p.title || "Untitled"}`} actions={actionsOf(p)} />
              </div>
            </li>
          ))}
        </ul>
      )}
      {confirm && (
        <DeleteSheet
          post={confirm}
          onDelete={() => {
            const p = confirm;
            setConfirm(null);
            focusAfterDelete.current = load.k === "ok" ? Math.max(0, load.posts.findIndex((x) => x.id === p.id)) : 0;
            void act(deletePost(p.id), "delete the post");
          }}
          onKeep={() => setConfirm(null)}
        />
      )}
    </main>
    </BlogShell>
  );
}

function DeleteSheet({ post, onDelete, onKeep }: { post: PostSummary; onDelete: () => void; onKeep: () => void }) {
  const ref = useDialog<HTMLDivElement>(onKeep);
  return (
    <div ref={ref} className="blog-sheet" role="alertdialog" aria-modal="true" aria-label="Delete post" tabIndex={-1}>
      <h2>Delete “{post.title || "Untitled"}”?</h2>
      <p>
        {post.status === "published" ? "It is public now and will stop being readable. " : ""}This removes the post, its versions and its images. It cannot be undone.
      </p>
      <div className="blog-actions">
        <button type="button" className="btn" onClick={onDelete}>
          Delete post
        </button>
        <button type="button" className="quiet" data-autofocus onClick={onKeep}>
          Keep it
        </button>
      </div>
    </div>
  );
}

import { useCallback, useEffect, useState } from "react";
import { createPost, deletePost, describeBlogError, duplicatePost, listPosts, unpublish } from "./blogApi";
import type { BlogResult } from "./blogApi";
import { useDialog } from "./useDialog";
import type { PostStatus, PostSummary } from "./types";

export const STATUS_WORDS: Record<PostStatus, string> = {
  draft: "Draft",
  checking: "Checking",
  ready: "Ready to publish",
  published: "Published",
  unpublished: "Unpublished",
};

type Load = { k: "loading" } | { k: "error"; why: string } | { k: "ok"; posts: PostSummary[] };

const day = (iso: string) => iso.slice(0, 10);

export function PostList({ onOpen, onChats }: { onOpen: (id: string) => void; onChats: () => void }) {
  const [load, setLoad] = useState<Load>({ k: "loading" });
  const [confirm, setConfirm] = useState<PostSummary | null>(null);
  const [note, setNote] = useState("");

  const refresh = useCallback(() => {
    const ctl = new AbortController();
    setLoad({ k: "loading" });
    void listPosts(ctl.signal).then((r) => {
      if (ctl.signal.aborted) return;
      setLoad(r.ok ? { k: "ok", posts: r.value } : { k: "error", why: describeBlogError(r, "load your posts") });
    });
    return () => ctl.abort();
  }, []);
  useEffect(() => refresh(), [refresh]);

  const act = async (p: Promise<BlogResult<unknown>>, what: string) => {
    setNote("");
    const r = await p;
    if (r.ok) return refresh();
    setNote(describeBlogError(r, what));
  };

  const create = async () => {
    const r = await createPost();
    if (r.ok) onOpen(r.value.id);
    else setNote(describeBlogError(r, "start a new post"));
  };

  return (
    <main className="blog-list">
      <header className="blog-bar">
        <button type="button" className="quiet" onClick={onChats}>
          Chats
        </button>
        <h1>My posts</h1>
        <button type="button" className="btn" onClick={() => void create()}>
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
              <button type="button" className="blog-row-main" onClick={() => onOpen(p.id)}>
                <span className="blog-row-title">{p.title || "Untitled"}</span>
                <span className={`chip chip-${p.status}`}>{STATUS_WORDS[p.status]}</span>
                {p.interactive && <span className="chip chip-ai">Interactive</span>}
                <span className="label">{day(p.status === "published" ? p.publishedAt : p.updatedAt)}</span>
              </button>
              <span className="blog-row-actions">
                {p.status === "published" && p.slug && (
                  <a className="quiet" href={`/b/${p.slug}`}>
                    View
                  </a>
                )}
                {p.status === "published" && (
                  <button type="button" className="quiet" onClick={() => void act(unpublish(p.id), "unpublish")}>
                    Unpublish
                  </button>
                )}
                <button type="button" className="quiet" onClick={() => void act(duplicatePost(p.id), "duplicate")}>
                  Duplicate
                </button>
                <button type="button" className="quiet" onClick={() => setConfirm(p)}>
                  Delete
                </button>
              </span>
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
            void act(deletePost(p.id), "delete the post");
          }}
          onKeep={() => setConfirm(null)}
        />
      )}
    </main>
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
        <button type="button" className="quiet" onClick={onKeep}>
          Keep it
        </button>
      </div>
    </div>
  );
}

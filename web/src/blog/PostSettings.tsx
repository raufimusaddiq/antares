import { useEffect, useState } from "react";
import { describeBlogError, mediaUrl, saveSettings } from "./blogApi";
import { useDialog } from "./useDialog";
import type { Post } from "./types";
import { renderPostCard } from "./share/postCard";
import { highlights, summaryText } from "./share/postContent";

/** The same pattern the server enforces (3-80, a-z 0-9 and hyphens). The server decides; this only saves a round trip. */
export const SLUG = /^[a-z0-9](?:[a-z0-9-]{1,78})[a-z0-9]$/;

/**
 * The story picture and link preview of this draft as they would be once published (spec 002 FR-015). It is drawn from the
 * draft's own title, summary, headings and cover, only for the author, and only to look at: no send, save or copy is offered.
 */
function SharePreview({ post, summary, slug, authorName }: { post: Post; summary: string; slug: string; authorName: string }) {
  const [img, setImg] = useState<string | "failed" | null>(null);
  const titled = post.title.trim() !== "";
  useEffect(() => {
    if (!titled) return setImg(null);
    let live = true;
    let url = "";
    const t = window.setTimeout(() => {
      void renderPostCard({
        title: post.title.trim(),
        summary: summaryText(summary, post.doc),
        highlights: highlights(post.doc),
        authorName,
        hasCover: false,
        coverUrl: mediaUrl(post.coverUrl),
        date: new Date(post.updatedAt || Date.now()),
        address: `${window.location.host}/b/${slug || "your-post"}`,
      }).then(
        (blob) => {
          if (!live) return;
          if (!blob) return setImg("failed");
          url = URL.createObjectURL(blob);
          setImg(url);
        },
        () => live && setImg("failed"),
      );
    }, 400); // while the author is typing the picture is not redrawn on every key
    return () => {
      live = false;
      window.clearTimeout(t);
      if (url) URL.revokeObjectURL(url);
    };
  }, [titled, post.title, post.doc, post.coverUrl, post.updatedAt, summary, slug, authorName]);
  return (
    <section className="blog-share-preview" aria-labelledby="share-preview-label">
      <span id="share-preview-label" className="label">
        Share preview
      </span>
      {!titled ? (
        <p className="label">Give the post a title to see how it will be shared.</p>
      ) : (
        <div className="blog-share-preview-row">
          {img === null && <p className="label" role="status">Drawing…</p>}
          {img === "failed" && <p className="label">This browser could not draw the preview.</p>}
          {img !== null && img !== "failed" && <img src={img} alt={`Preview of the story picture for ${post.title}`} />}
          <p className="label">Preview only. This is the story picture made from your title, summary, headings and cover. It changes when you edit them, and it can be shared only once the post is published.</p>
        </div>
      )}
    </section>
  );
}

export function PostSettings({ post, onSaved, onClose, authorName = "" }: { post: Post; onSaved: (p: Post) => void; onClose: () => void; authorName?: string }) {
  const ref = useDialog<HTMLDivElement>(onClose);
  const [slug, setSlug] = useState(post.slug);
  const [summary, setSummary] = useState(post.summary);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const bad = slug !== "" && !SLUG.test(slug);

  const submit = async () => {
    setBusy(true);
    setError("");
    const r = await saveSettings(post.id, { slug, summary });
    setBusy(false);
    if (r.ok) return onSaved(r.value), onClose();
    setError(r.status === 409 ? "That address is already used by another published post." : r.status === 400 || r.status === 422 ? "That address cannot be used. Use 3-80 letters, digits or hyphens." : describeBlogError(r, "save the settings"));
  };

  return (
    <div ref={ref} className="blog-sheet" role="dialog" aria-modal="true" aria-label="Post settings" tabIndex={-1}>
      <h2>Post settings</h2>
      <label>
        Address
        <span className="label">/b/</span>
        <input value={slug} onChange={(e) => setSlug(e.target.value.toLowerCase())} aria-invalid={bad} aria-describedby="slug-help" />
      </label>
      <p id="slug-help" className={bad ? "blog-error" : "label"}>
        {bad ? "Use 3-80 lowercase letters, digits or hyphens." : "Shown in the post's web address. It must be unique among published posts."}
      </p>
      <label>
        Summary
        <textarea value={summary} maxLength={300} rows={3} onChange={(e) => setSummary(e.target.value)} />
      </label>
      <SharePreview post={post} summary={summary} slug={slug} authorName={authorName || "You"} />
      {error && (
        <p className="blog-error" role="alert">
          {error}
        </p>
      )}
      <div className="blog-actions">
        <button type="button" className="btn" disabled={busy || bad} onClick={() => void submit()}>
          Save
        </button>
        <button type="button" className="quiet" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  );
}

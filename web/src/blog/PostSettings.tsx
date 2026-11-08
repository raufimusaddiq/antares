import { useState } from "react";
import { describeBlogError, saveSettings } from "./blogApi";
import type { Post } from "./types";

/** The same pattern the server enforces (3-80, a-z 0-9 and hyphens). The server decides; this only saves a round trip. */
export const SLUG = /^[a-z0-9](?:[a-z0-9-]{1,78})[a-z0-9]$/;

export function PostSettings({ post, onSaved, onClose }: { post: Post; onSaved: (p: Post) => void; onClose: () => void }) {
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
    <div className="blog-sheet" role="dialog" aria-modal="true" aria-label="Post settings">
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

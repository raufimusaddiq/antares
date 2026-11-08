import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { cancelCheck, decideBlock, describeBlogError, getCheck, publish, startCheck } from "./blogApi";
import { SourceList } from "./Sources";
import type { Capabilities, Check, CheckBlock, Post } from "./types";

const ReaderBlock = lazy(() => import("./ReaderBlock"));

// Publish: choose, check, preview, publish. A failed, cancelled, timed-out or disconnected check is shown as itself,
// never as completed, and nothing is published until the author says so (FR-012, FR-013). The wording follows
// specs/001-blog-cms-openui-publish/contracts/check-envelope.md (Failure mapping).

const POLL_MS = 2000;

export function failureWords(c: Check): string {
  switch (c.state) {
    case "timed_out":
      return "The check did not finish: the assistant timed out. It may or may not have started.";
    case "cancelled":
      return "Cancelled before the check finished.";
    case "out_of_date":
      return "The draft changed. Run the check again.";
    default:
      return c.reason === "disconnected" ? "Connection to the assistant was lost." : c.reason === "unusable" ? "The assistant's reply could not be used." : "The check did not finish: the assistant reported an error.";
  }
}

type Phase = { k: "choose" } | { k: "starting" } | { k: "checking" } | { k: "preview" } | { k: "published"; slug: string } | { k: "stopped" };

export interface PublishFlowProps {
  post: Post;
  caps: Capabilities | null;
  flush: () => Promise<void>;
  onPost: (p: Post) => void;
  onBackToEditing: () => void;
  onMyPosts: () => void;
}

export function PublishFlow({ post, caps, flush, onPost, onBackToEditing, onMyPosts }: PublishFlowProps) {
  const [phase, setPhase] = useState<Phase>(() => (!post.check ? { k: "choose" } : post.check.state === "running" ? { k: "checking" } : post.check.state === "ready" ? { k: "preview" } : { k: "stopped" }));
  const [check, setCheck] = useState<Check | null>(post.check);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [acks, setAcks] = useState<Record<string, boolean>>({});
  const canCheck = caps?.check === true;
  const ready = post.title.trim() !== "";

  const apply = useCallback((c: Check) => {
    setCheck(c);
    setPhase(c.state === "running" ? { k: "checking" } : c.state === "ready" ? { k: "preview" } : { k: "stopped" });
  }, []);

  useEffect(() => {
    if (phase.k !== "checking") return;
    const ctl = new AbortController();
    const t = setInterval(() => {
      void getCheck(post.id, ctl.signal).then((r) => {
        if (ctl.signal.aborted) return;
        if (r.ok) apply(r.value);
        else if (r.status !== 0) {
          setError(describeBlogError(r, "read the check"));
        }
      });
    }, POLL_MS);
    return () => {
      ctl.abort();
      clearInterval(t);
    };
  }, [phase.k, post.id, apply]);

  const begin = async () => {
    setError("");
    setPhase({ k: "starting" });
    await flush(); // the check reads the saved version
    const r = await startCheck(post.id);
    if (r.ok) return apply(r.value);
    setPhase({ k: "choose" });
    setError(r.status === 409 ? "A check is already running for this post." : r.code === "CAPABILITY_UNAVAILABLE" ? "Checking is not available right now." : describeBlogError(r, "start the check"));
  };

  const go = async (mode: "enriched" | "original") => {
    setBusy(true);
    setError("");
    if (mode === "original") await flush();
    const r = await publish(post.id, mode);
    setBusy(false);
    if (r.ok) {
      onPost(r.value);
      return setPhase({ k: "published", slug: r.value.slug });
    }
    setError(r.status === 409 ? "The address is taken by another published post, or the draft changed. Open Post settings." : r.status === 422 || r.status === 400 ? "Add a title and an address before publishing." : describeBlogError(r, "publish"));
  };

  const decide = async (b: CheckBlock, d: "accepted" | "rejected") => {
    if (!check) return;
    const r = await decideBlock(post.id, b.id, d, d === "accepted" && b.needsAck ? true : undefined);
    if (!r.ok) return setError(describeBlogError(r, "save that choice"));
    setCheck({ ...check, blocks: check.blocks.map((x) => (x.id === b.id ? r.value : x)) });
  };

  if (phase.k === "published")
    return (
      <main className="blog-flow">
        <h1>Your post is live</h1>
        <p>Anyone can read it, without an account.</p>
        <div className="blog-actions">
          <a className="btn" href={`/b/${phase.slug}`}>
            View post
          </a>
          <button type="button" className="quiet" onClick={onMyPosts}>
            My posts
          </button>
        </div>
      </main>
    );

  if (phase.k === "checking" || phase.k === "starting")
    return (
      <main className="blog-flow" aria-busy="true">
        <h1>Checking your writing</h1>
        <p role="status">Your assistant is reading the post, looking for data to add and finding sources. This can take a minute or two.</p>
        <p className="label">Nothing is published yet. Your original is unchanged.</p>
        <div className="blog-actions">
          <button
            type="button"
            className="quiet"
            disabled={phase.k === "starting"}
            onClick={() => void cancelCheck(post.id).then((r) => (r.ok ? apply(r.value) : setError(describeBlogError(r, "cancel the check"))))}
          >
            Cancel
          </button>
        </div>
        {error && <p className="blog-error" role="alert">{error}</p>}
      </main>
    );

  if (phase.k === "stopped" && check)
    return (
      <main className="blog-flow">
        <h1>{check.state === "out_of_date" ? "The draft changed" : "The check did not finish"}</h1>
        <p role="alert">{failureWords(check)}</p>
        <p className="label">Nothing was published.</p>
        <div className="blog-actions">
          {canCheck && (
            <button type="button" className="btn" onClick={() => void begin()}>
              Try again
            </button>
          )}
          <button type="button" className="quiet" disabled={busy || !ready} onClick={() => void go("original")}>
            Publish original
          </button>
          <button type="button" className="quiet" onClick={onBackToEditing}>
            Back to editing
          </button>
        </div>
        {error && <p className="blog-error" role="alert">{error}</p>}
      </main>
    );

  if (phase.k === "preview" && check) {
    const taken = check.blocks.filter((b) => b.decision === "accepted").length;
    return (
      <main className="blog-flow blog-preview">
        <h1>Preview</h1>
        <p>The assistant proposed {check.blocks.length} {check.blocks.length === 1 ? "block" : "blocks"}. Your writing is unchanged. Accept the ones you want.</p>
        {check.notices.map((n) => (
          <p key={n} className="blog-notice" role="status">{n}</p>
        ))}
        {check.notes.length > 0 && (
          <section aria-label="Notes">
            <h2>Notes</h2>
            <ul>
              {check.notes.map((n, i) => (
                <li key={i}><span className="chip">{n.kind}</span> {n.text}</li>
              ))}
            </ul>
          </section>
        )}
        {check.blocks.map((b) => {
          const blocked = b.needsAck && !acks[b.id];
          return (
            <section key={b.id} className="blog-block" aria-label={`Proposed block: ${b.kind}`}>
              <span className="chip chip-ai">AI-added</span>
              <Suspense fallback={<p className="label">Loading…</p>}>
                <ReaderBlock markup={b.markup} fallback={b.fallback} />
              </Suspense>
              <SourceList sources={b.sources} needsAck={b.needsAck} acked={acks[b.id] === true} onAck={(v) => setAcks({ ...acks, [b.id]: v })} />
              <div className="blog-actions">
                <button type="button" className="btn" aria-pressed={b.decision === "accepted"} disabled={blocked} onClick={() => void decide(b, "accepted")}>
                  {b.decision === "accepted" ? "Accepted" : "Accept"}
                </button>
                <button type="button" className="quiet" aria-pressed={b.decision === "rejected"} onClick={() => void decide(b, "rejected")}>
                  {b.decision === "rejected" ? "Rejected" : "Reject"}
                </button>
              </div>
            </section>
          );
        })}
        {error && <p className="blog-error" role="alert">{error}</p>}
        <div className="blog-actions">
          <button type="button" className="btn" disabled={busy || taken === 0} onClick={() => void go("enriched")}>
            Publish with {taken} {taken === 1 ? "block" : "blocks"}
          </button>
          <button type="button" className="quiet" disabled={busy || !ready} onClick={() => void go("original")}>
            Publish original
          </button>
          <button type="button" className="quiet" onClick={onBackToEditing}>
            Back to editing
          </button>
        </div>
      </main>
    );
  }

  return (
    <main className="blog-flow">
      <h1>Publish</h1>
      {!ready && <p className="blog-error" role="alert">Give the post a title before publishing.</p>}
      <p>You can publish exactly what you wrote, or have your assistant check it first and propose interactive blocks with sourced data. You approve each one.</p>
      {!canCheck && <p className="blog-notice" role="status">Checking is not available right now, so only your original can be published.</p>}
      <div className="blog-actions">
        {canCheck && (
          <button type="button" className="btn" disabled={!ready} onClick={() => void begin()}>
            Check and add blocks
          </button>
        )}
        <button type="button" className={canCheck ? "quiet" : "btn"} disabled={busy || !ready} onClick={() => void go("original")}>
          Publish my original
        </button>
        <button type="button" className="quiet" onClick={onBackToEditing}>
          Back to editing
        </button>
      </div>
      {error && <p className="blog-error" role="alert">{error}</p>}
    </main>
  );
}

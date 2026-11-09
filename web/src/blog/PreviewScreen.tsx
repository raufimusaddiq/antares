import { lazy, Suspense, useState } from "react";
import { DocView, SourcesLine } from "./DocView";
import { AckGate, SourceDrawer } from "./Sources";
import type { Check, CheckBlock, CheckNote, Post } from "./types";

const ReaderBlock = lazy(() => import("./ReaderBlock"));

const NOTE_WORDS: Record<CheckNote["kind"], string> = { clarity: "Clarity", fact: "Fact", "missing-context": "Missing context", unsourced: "Unsourced" };

export interface PreviewProps {
  post: Post;
  check: Check;
  acks: Record<string, boolean>;
  onAck: (blockId: string, v: boolean) => void;
  busy: boolean;
  ready: boolean;
  error: string;
  onDecide: (b: CheckBlock, d: "accepted" | "rejected") => void;
  onAcceptAll: () => void;
  onPublishAccepted: () => void;
  onOriginal: () => void;
}

/**
 * Review before publishing, as the design has it: the author's original, the enriched preview with Accept and Reject on each
 * block, and the assistant's notes with the sources of the block in focus. Nothing is public until the author publishes.
 * "Accept all and publish" is the way through when nothing has been decided; once the author decides a block it becomes
 * "Publish with N blocks" (the design only draws the first state).
 */
export function PreviewScreen({ post, check, acks, onAck, busy, ready, error, onDecide, onAcceptAll, onPublishAccepted, onOriginal }: PreviewProps) {
  const [selected, setSelected] = useState<string | null>(null);
  const blocks = check.blocks;
  const focus = blocks.find((b) => b.id === selected) ?? blocks[0];
  const taken = blocks.filter((b) => b.decision === "accepted").length;
  const decided = blocks.some((b) => b.decision !== "proposed");
  // Blocks that still need the author's tick before they can be accepted (one already accepted had it when it was).
  const waiting = blocks.filter((b) => b.needsAck && b.decision !== "accepted" && acks[b.id] !== true).length;
  const none = blocks.length === 0;

  return (
    <main className="blog-flow blog-review">
      <header className="blog-review-head">
        <div>
          <h1 tabIndex={-1}>Review before publishing</h1>
          <p className="label">Everything marked AI-added is a proposal. Nothing is public until you publish.</p>
        </div>
        <div className="blog-review-actions">
          <button type="button" className={none ? "btn" : "quiet"} disabled={busy || !ready} onClick={onOriginal}>
            Publish my original
          </button>
          {!none &&
            (decided ? (
              <button type="button" className="btn" disabled={busy || taken === 0} onClick={onPublishAccepted}>
                Publish with {taken} {taken === 1 ? "block" : "blocks"}
              </button>
            ) : (
              <button type="button" className="btn" disabled={busy || waiting > 0} aria-describedby={waiting > 0 ? "accept-all-hint" : undefined} onClick={onAcceptAll}>
                Accept all and publish
              </button>
            ))}
        </div>
      </header>
      {!decided && waiting > 0 && (
        <p id="accept-all-hint" className="label blog-review-hint">
          Tick “I have checked these sources myself” on {waiting} {waiting === 1 ? "block" : "blocks"} before accepting all.
        </p>
      )}
      {check.notices.map((n) => (
        <p key={n} className="blog-notice" role="status">
          {n}
        </p>
      ))}
      {none && <p className="blog-notice" role="status">The assistant proposed no blocks. Your writing is unchanged.</p>}
      {error && (
        <p className="blog-error" role="alert">
          {error}
        </p>
      )}

      <div className="blog-review-cols">
        <section className="blog-original" aria-label="Your original" tabIndex={0}>
          <p className="label">Your original</p>
          <h2>{post.title}</h2>
          <DocView doc={post.doc} />
        </section>

        <section className="blog-enriched" aria-label="Enriched preview">
          <p className="label">Enriched preview</p>
          {blocks.map((b) => (
            <article key={b.id} className={b.id === focus?.id ? "blog-block selected" : "blog-block"} aria-label={`Proposed block: ${b.kind}`} onClickCapture={() => setSelected(b.id)} onFocusCapture={() => setSelected(b.id)}>
              <div className="blog-block-head">
                <span className="chip chip-ai">AI-added</span>
                <span className="blog-block-decide">
                  <button type="button" className="quiet" aria-pressed={b.decision === "rejected"} disabled={busy} onClick={() => onDecide(b, "rejected")}>
                    {b.decision === "rejected" ? "Rejected" : "Reject"}
                  </button>
                  <button type="button" className="btn" aria-pressed={b.decision === "accepted"} disabled={busy || (b.needsAck && acks[b.id] !== true && b.decision !== "accepted")} onClick={() => onDecide(b, "accepted")}>
                    {b.decision === "accepted" ? "Accepted" : "Accept"}
                  </button>
                </span>
              </div>
              <Suspense fallback={<p className="label">Loading…</p>}>
                <ReaderBlock markup={b.markup} fallback={b.fallback} />
              </Suspense>
              <SourcesLine sources={b.sources} />
              {b.needsAck && <AckGate count={b.sources.filter((s) => !s.verified).length} acked={acks[b.id] === true || b.decision === "accepted"} locked={b.decision === "accepted"} onAck={(v) => onAck(b.id, v)} />}
            </article>
          ))}
        </section>

        <div className="blog-review-side">
          {check.notes.length > 0 && (
            <section className="blog-notes" aria-labelledby="notes-h">
              <h3 id="notes-h">Notes from the assistant</h3>
              {check.notes.map((n, i) => (
                <div key={i} className={n.kind === "unsourced" ? "blog-note blog-note-warn" : "blog-note"}>
                  <p className="label">{NOTE_WORDS[n.kind]}</p>
                  <p>{n.text}</p>
                </div>
              ))}
            </section>
          )}
          {focus && <SourceDrawer sources={focus.sources} />}
        </div>
      </div>
    </main>
  );
}

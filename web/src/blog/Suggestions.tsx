import { useEffect, useRef, useState } from "react";
import { decideEdit, describeBlogError } from "./blogApi";
import type { DraftBridge } from "./draftBridge";
import type { EditSuggestion } from "./types";

// The editor's suggestions for the author's own text (specs/006-ai-editor-blog, US3). Accepting applies one ordinary, undoable change in the editor and
// then records the choice; rejecting only records it. A suggestion whose passage is gone is shown as no longer applicable and is never applied.

export interface SuggestionsProps {
  postId: string;
  edits: EditSuggestion[];
  bridge: DraftBridge | null;
  /** Any value that changes when the draft does (the document), so "no longer applicable" is always current. */
  doc: unknown;
  onDecided: (e: EditSuggestion) => void;
  /** The list's own heading; the editor panel names itself and leaves this out. */
  hideHeading?: boolean;
  /** The Publish review screen's smaller cards (the design shows each one as one line and its buttons). */
  compact?: boolean;
}

export function Suggestions({ postId, edits, bridge, doc, onDecided, hideHeading = false, compact = false }: SuggestionsProps) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [undone, setUndone] = useState<Set<string>>(new Set());
  const [focusNext, setFocusNext] = useState<string | "heading" | null>(null);
  const refs = useRef<Map<string, HTMLElement>>(new Map());
  const head = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (focusNext === null) return;
    (focusNext === "heading" ? (head.current ?? document.getElementById("blog-panel-h")) : refs.current.get(focusNext))?.focus();
    setFocusNext(null);
  }, [focusNext, edits]);

  void doc; // a new value re-renders the list, and applicability is read from the editor each time
  const applicable = (e: EditSuggestion) => (bridge ? bridge.applicable(e) : true);
  const next = (id: string) => {
    const rest = edits.filter((e) => e.decision === "open" && e.id !== id);
    const after = rest.find((e) => edits.indexOf(e) > edits.findIndex((x) => x.id === id)) ?? rest[0];
    setFocusNext(after ? after.id : "heading");
  };

  const accept = async (e: EditSuggestion) => {
    if (!bridge) return;
    setError("");
    setBusy(e.id);
    if (bridge.apply(e) === "not_applicable") {
      setBusy(null);
      return setError("That passage changed, so the suggestion was not applied.");
    }
    const r = await decideEdit(postId, e.id, "accepted");
    setBusy(null);
    if (!r.ok) {
      bridge.undo(e.id); // the choice could not be saved: leave the text as it was, and say so
      return setError(describeBlogError(r, "save that choice") + " Nothing was changed.");
    }
    onDecided(r.value);
    next(e.id);
  };
  const reject = async (e: EditSuggestion) => {
    setError("");
    setBusy(e.id);
    const r = await decideEdit(postId, e.id, "rejected");
    setBusy(null);
    if (!r.ok) return setError(describeBlogError(r, "save that choice"));
    onDecided(r.value);
    next(e.id);
  };
  const undo = async (e: EditSuggestion) => {
    if (!bridge?.undo(e.id)) return;
    setUndone((s) => new Set(s).add(e.id));
    const r = await decideEdit(postId, e.id, "rejected");
    if (r.ok) onDecided(r.value);
  };

  const n = (e: EditSuggestion) => edits.indexOf(e) + 1;
  return (
    <section className={compact ? "suggestions suggestions-compact blog-notes" : "suggestions"} aria-labelledby={hideHeading ? undefined : `sg-h-${postId}`} aria-label={hideHeading ? "Suggestions" : undefined}>
      {!hideHeading && (
        <h3 id={`sg-h-${postId}`} ref={head} tabIndex={-1}>
          Suggestions ({edits.length})
        </h3>
      )}
      {!compact && <p className="label">Nothing changes in your draft until you accept one.</p>}
      {error && (
        <p className="blog-error" role="alert">
          {error}
        </p>
      )}
      {edits.map((e) => {
        const here = applicable(e);
        const state = undone.has(e.id) ? "undone" : e.decision === "accepted" ? "accepted" : e.decision === "rejected" ? "rejected" : here ? "open" : "gone";
        return (
          <article key={e.id} className={`suggestion suggestion-${e.kind} suggestion-${state}`} data-suggestion={e.id} aria-label={`Suggestion ${n(e)}`}>
            <p className="suggestion-head">
              <span className={e.kind === "insert" ? "chip chip-warn" : "chip chip-info"}>{state === "accepted" ? "Accepted" : state === "rejected" || state === "undone" ? (state === "undone" ? "Undone" : "Rejected") : state === "gone" ? "No longer applicable" : e.kind === "insert" ? "Written by the assistant" : "Suggested edit"}</span>
              <span className="label">{e.kind === "insert" ? `After “${e.anchor || "this part"}”` : e.anchor.slice(0, 40)}</span>
            </p>
            {state === "gone" && <p>You changed this passage after the check, so the suggestion no longer fits. Nothing was applied.</p>}
            {state === "open" && compact && (
              <p className="suggestion-line">
                {e.kind === "replace" ? `“${e.original}” → “${e.proposed}”` : `${e.proposed.replace(/\s+/g, " ").slice(0, 140)}${e.proposed.length > 140 ? "…" : ""}`}
              </p>
            )}
            {state === "open" && !compact && e.kind === "replace" && (
              <>
                <p className="suggestion-old">
                  <span className="label">You wrote</span> <s>{e.original}</s>
                </p>
                <p className="suggestion-new">
                  <span className="label">Proposed</span> {e.proposed}
                </p>
              </>
            )}
            {state === "open" && !compact && e.kind === "insert" && (
              <div className="suggestion-new">
                <span className="label">Proposed paragraph{e.proposed.includes("\n\n") ? "s" : ""}</span>
                {e.proposed.split(/\n\s*\n/).map((para, i) => (
                  <p key={i}>{para}</p>
                ))}
              </div>
            )}
            {state === "open" && !compact && <p className="label">Why: {e.reason}</p>}
            <div className="actions">
              {state === "open" && (
                <>
                  <button type="button" className="btn" ref={(el) => { if (el) refs.current.set(e.id, el); else refs.current.delete(e.id); }} aria-label={`Accept suggestion ${n(e)}`} disabled={busy !== null} onClick={() => void accept(e)}>
                    Accept
                  </button>
                  <button type="button" className="quiet" aria-label={`Reject suggestion ${n(e)}`} disabled={busy !== null} onClick={() => void reject(e)}>
                    Reject
                  </button>
                </>
              )}
              {state === "accepted" && (
                <>
                  <span className="label">Applied to your draft as one change. Undo works until you change something else.</span>
                  {bridge?.canUndo(e.id) && (
                    <button type="button" className="quiet" aria-label={`Undo suggestion ${n(e)}`} onClick={() => void undo(e)}>
                      Undo
                    </button>
                  )}
                </>
              )}
            </div>
          </article>
        );
      })}
    </section>
  );
}

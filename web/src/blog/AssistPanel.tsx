import { useEffect, useRef } from "react";
import { useEditorState } from "@tiptap/react";
import type { Editor } from "@tiptap/react";
import { CheckProgressView } from "./CheckProgress";
import type { DraftBridge } from "./draftBridge";
import { Suggestions } from "./Suggestions";
import type { Check } from "./types";
import { failureWords } from "./PublishFlow";
import { useCheckRun } from "./useCheckRun";

// "Check with the editor" (specs/006-ai-editor-blog, US3): the same check as the one at Publish, run while writing, with its progress and its suggestions beside the
// draft (below it on a phone). Starting it saves the draft first; nothing in the draft changes until the author accepts a suggestion.

const timeOf = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
};

export interface AssistPanelProps {
  postId: string;
  initial: Check | null;
  editor: Editor | null;
  bridge: DraftBridge | null;
  flush: () => Promise<boolean>;
  /** The Publish screen is open over the editor: the panel stops polling (one surface reads a check at a time). */
  paused: boolean;
  canCheck: boolean;
  onClose: () => void;
}

export function AssistPanel({ postId, initial, editor, bridge, flush, paused, canCheck, onClose }: AssistPanelProps) {
  const run = useCheckRun(postId, initial, flush, paused);
  const doc = useEditorState({ editor, selector: ({ editor: e }) => e?.state.doc ?? null });
  const head = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    head.current?.scrollIntoView?.({ block: "start" }); // below the draft on a narrow screen: bring it into view, then move focus into it
    head.current?.focus({ preventScroll: true });
  }, []);
  const c = run.check;
  const edits = c?.edits ?? [];
  return (
    <aside className="blog-panel" id="blog-panel" aria-labelledby="blog-panel-h">
      <div className="blog-panel-bar">
        <h2 id="blog-panel-h" ref={head} tabIndex={-1}>
          {run.phase === "running" || run.phase === "starting" ? "Checking with the editor" : run.phase === "ready" ? "From the editor" : run.phase === "stopped" ? "The check did not finish" : "Check with the editor"}
        </h2>
        <button type="button" className="quiet" aria-label="Close the editor panel" onClick={onClose}>
          Close
        </button>
      </div>
      {run.error && (
        <p className="blog-error" role="alert">
          {run.error}
        </p>
      )}
      {run.limit && (
        <div className="notice-card" role="alert">
          <h3>No more checks right now</h3>
          <p>
            {run.limit.running ? "Two checks are already running. Wait for one to finish." : `You have used your checks of the last 24 hours.${run.limit.retryAt && timeOf(run.limit.retryAt) ? ` You can check again after ${timeOf(run.limit.retryAt)}.` : ""}`} You can still publish your original at any time.
          </p>
        </div>
      )}
      {(run.phase === "idle" || run.phase === "stopped") && !run.limit && (
        <>
          {c && run.phase === "stopped" ? <p role="alert">{failureWords(c)} Nothing was changed in your draft and nothing was published.</p> : <p>The editor reads your post, looks things up where it helps, and suggests changes to your text. It can also write a first version of each section from your headings and points.</p>}
          {!canCheck && <p className="label">Checking is not available right now.</p>}
          <button type="button" className="btn" disabled={!canCheck} onClick={() => void run.start()}>
            {run.phase === "stopped" ? "Try again" : "Check my post"}
          </button>
        </>
      )}
      {(run.phase === "starting" || run.phase === "running") && (
        <>
          <p className="label">Nothing in your draft changes while this runs. You decide on every suggestion.</p>
          <CheckProgressView check={run.phase === "starting" ? null : c} cancelDisabled={run.phase === "starting"} onCancel={() => void run.cancel()} />
        </>
      )}
      {run.phase === "ready" && c && (
        <>
          <Suggestions postId={postId} edits={edits} bridge={bridge} doc={doc ?? null} onDecided={(e) => run.setEdit(e.id, e)} />
          {c.notes.length > 0 && <p className="label">{c.notes.length} {c.notes.length === 1 ? "note" : "notes"} from the editor are on the Publish screen.</p>}
          <button type="button" className="quiet" onClick={() => void run.start()}>
            Check again
          </button>
        </>
      )}
    </aside>
  );
}

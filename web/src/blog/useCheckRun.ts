import { useCallback, useEffect, useState } from "react";
import { cancelCheck, describeBlogError, getCheck, startCheck } from "./blogApi";
import type { Check } from "./types";

// One check of one post, started from the editor panel (specs/006-ai-editor-blog): start (after the draft is saved), poll while it runs (one request at a
// time, a refusal ends the polling), cancel. The Publish flow keeps its own lifecycle; both read the same server object, the post's latest check.

export const POLL_MS = 1500;
export const UNSAVED = "Your latest changes are not saved yet, so the check would read an older version. Wait for “Saved” and try again.";

export type RunPhase = "idle" | "starting" | "running" | "ready" | "stopped";

export interface RunLimit {
  retryAt?: string;
  running?: boolean;
}

const phaseOf = (c: Check | null): RunPhase => (!c ? "idle" : c.state === "running" ? "running" : c.state === "ready" ? "ready" : "stopped");

export function useCheckRun(postId: string, initial: Check | null, flush: () => Promise<boolean>, paused = false) {
  const [check, setCheck] = useState<Check | null>(initial);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const [limit, setLimit] = useState<RunLimit | null>(null);
  const phase: RunPhase = starting ? "starting" : phaseOf(check);

  useEffect(() => {
    if (paused || phase !== "running") return;
    const ctl = new AbortController();
    let waiting = false;
    let halted = false;
    const t = setInterval(() => {
      if (waiting || halted) return;
      waiting = true;
      void getCheck(postId, ctl.signal).then((r) => {
        waiting = false;
        if (ctl.signal.aborted) return;
        if (r.ok) setCheck(r.value);
        else if (r.status !== 0) {
          halted = true;
          setError(describeBlogError(r, "read the check") + " Reload the page to look again.");
        }
      });
    }, POLL_MS);
    return () => {
      ctl.abort();
      clearInterval(t);
    };
  }, [phase, paused, postId]);

  const start = useCallback(async () => {
    setError("");
    setLimit(null);
    setStarting(true);
    if (!(await flush())) { // the check reads the saved draft
      setStarting(false);
      return setError(UNSAVED);
    }
    const r = await startCheck(postId);
    setStarting(false);
    if (r.ok) return setCheck(r.value);
    if (r.code === "LIMIT_REACHED") return setLimit({ retryAt: r.retryAt, running: r.running });
    setError(r.status === 409 ? "A check is already running for this post." : r.code === "CAPABILITY_UNAVAILABLE" ? "Checking is not available right now." : r.code === "TOO_LONG_TO_CHECK" ? "This post is longer than the assistant can read in one check. Publish your original, or shorten it." : describeBlogError(r, "start the check"));
  }, [postId, flush]);

  const cancel = useCallback(async () => {
    const r = await cancelCheck(postId);
    if (r.ok) setCheck(r.value);
    else setError(describeBlogError(r, "cancel the check"));
  }, [postId]);

  const setEdit = useCallback((id: string, next: NonNullable<Check["edits"]>[number]) => setCheck((c) => (c ? { ...c, edits: (c.edits ?? []).map((e) => (e.id === id ? next : e)) } : c)), []);
  return { check, phase, error, limit, start, cancel, setEdit, dismiss: () => { setError(""); setLimit(null); } };
}

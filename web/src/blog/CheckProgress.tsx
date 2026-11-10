import { useEffect, useState } from "react";
import type { Check, CheckStage } from "./types";

// What the author sees while a check runs (specs/006-ai-editor-blog, US2). The step comes from the server, read from the assistant's own events: the
// screen names it, counts the time in the server's clock and says how long ago the last sign of life was. It never draws a percentage: nothing here is measured.

const STALLED_MS = 45_000;
const ORDER: CheckStage[] = ["reading", "searching", "writing"];
const WORDS: Record<CheckStage, string> = { reading: "Reading your post", searching: "Looking things up", writing: "Writing suggestions" };

export function durationWords(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  return s < 60 ? `${s} s` : `${Math.floor(s / 60)} min ${s % 60} s`;
}

export interface CheckProgressProps {
  check: Check | null;
  onCancel: () => void;
  cancelDisabled: boolean;
}

export function CheckProgressView({ check, onCancel, cancelDisabled }: CheckProgressProps) {
  const p = check?.progress ?? null;
  // The server's clock at the moment of the last read, against the browser's: the clock ticks from there, so a wrong clock here changes nothing.
  const [skew, setSkew] = useState(0);
  const [, tick] = useState(0);
  useEffect(() => {
    if (p?.now) setSkew(Date.parse(p.now) - Date.now());
  }, [p?.now]);
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  const now = Date.now() + skew;
  const stage: CheckStage = p?.stage ?? "reading";
  const at = ORDER.indexOf(stage);
  const elapsed = p ? now - Date.parse(p.startedAt) : 0;
  const quiet = p ? now - Date.parse(p.lastEventAt) : 0;
  const stalled = p !== null && quiet >= STALLED_MS;
  const lookups = p?.lookups ?? 0;
  const label = (s: CheckStage) => (s === "searching" ? (lookups > 0 ? `${WORDS.searching} · ${lookups} ${lookups === 1 ? "lookup" : "lookups"}` : at > 1 ? `${WORDS.searching} · none needed` : WORDS.searching) : WORDS[s]);
  return (
    <div className="check-progress">
      <ol className="steps" aria-label="Steps of the check">
        {ORDER.map((s, i) => {
          const state = i < at ? "done" : i === at ? "current" : "pending";
          return (
            <li key={s} className={`step step-${state}`} aria-current={state === "current" ? "step" : undefined}>
              <span className="step-mark" aria-hidden="true">
                {state === "done" ? "✓" : state === "current" ? "•" : ""}
              </span>
              <span>{label(s)}</span>
            </li>
          );
        })}
      </ol>
      {/* Announced once per step (and once when it goes quiet), never every second. */}
      <p className="blog-sr" role="status" aria-live="polite">
        {WORDS[stage]}
        {stalled ? ". Nothing new for a while" : ""}
      </p>
      <p className="step-clock" aria-hidden="true">
        {durationWords(elapsed)} so far{p ? ` · last sign of life ${durationWords(quiet)} ago` : ""}
      </p>
      {stalled && <p className="step-stalled">Still working. Nothing new for {durationWords(quiet)}. You can keep waiting or cancel.</p>}
      <div className="blog-actions">
        <button type="button" className="quiet" disabled={cancelDisabled} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

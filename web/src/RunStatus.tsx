import { useEffect, useState } from "react";

/**
 * The live reply's status line: what it is doing, and how long that step has gone on, so a long step reads as alive
 * rather than stuck. The clock restarts when the step changes, and on a reload (it counts what this page has seen).
 * Only the step is announced; the ticking clock is not.
 */
export function RunStatus({ text }: { text: string }) {
  const [step, setStep] = useState({ text, since: Date.now() });
  const [now, setNow] = useState(step.since);
  if (step.text !== text) setStep({ text, since: Date.now() });
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, []);
  const s = Math.max(0, Math.floor((now - step.since) / 1000));
  return (
    <p className="label run-status">
      <span role="status">{text}</span>
      {s >= 5 && (
        <span className="elapsed" aria-hidden="true">
          {Math.floor(s / 60)}:{String(s % 60).padStart(2, "0")}
        </span>
      )}
    </p>
  );
}

import { useCallback, useEffect, useRef, useState } from "react";
import { describeAdminCode, isForbidden, type AdminResult } from "./adminApi";

// ---- load state: every admin screen renders all four of these ----------------------

export type Load<T> = { kind: "loading" } | { kind: "ready"; value: T } | { kind: "error"; message: string } | { kind: "forbidden" };

export function useLoad<T>(fetcher: (signal: AbortSignal) => Promise<AdminResult<T>>, key: string): [Load<T>, () => void] {
  const [state, setState] = useState<Load<T>>({ kind: "loading" });
  const [tick, setTick] = useState(0);
  const lastKey = useRef(key);
  useEffect(() => {
    const ctl = new AbortController();
    // A reload of the same thing keeps what is on screen (focus, scroll and the person's place stay); a different thing starts from "loading".
    const same = lastKey.current === key;
    lastKey.current = key;
    setState((s) => (same && s.kind === "ready" ? s : { kind: "loading" }));
    void fetcher(ctl.signal).then((r) => {
      if (ctl.signal.aborted) return;
      if (r.ok) setState({ kind: "ready", value: r.value });
      else setState(isForbidden(r) ? { kind: "forbidden" } : { kind: "error", message: describeAdminCode(r.code, r.status) });
    });
    return () => ctl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, tick]);
  return [state, useCallback(() => setTick((t) => t + 1), [])];
}

export function Forbidden() {
  return (
    <section aria-labelledby="forbidden-h">
      <h1 id="forbidden-h">Administrators only</h1>
      <p className="notice" role="alert">
        This account is not allowed to use the admin area. The server decided this; nothing on this page was loaded.
      </p>
      <p className="label">If you should have access, ask the person who runs this service.</p>
    </section>
  );
}

export function Problem({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <p className="notice" role="alert">
      {message}{" "}
      <button type="button" className="quiet" onClick={onRetry}>
        Try again
      </button>
    </p>
  );
}

export function Loading({ what }: { what: string }) {
  return (
    <p className="label" role="status" aria-busy="true">
      Loading {what}…
    </p>
  );
}

/** Deterministic UTC, so a screenshot, a test and a user in another zone all read the same. */
export function when(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : `${d.toISOString().slice(0, 16).replace("T", " ")} UTC`;
}


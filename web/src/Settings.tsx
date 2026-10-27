import { useEffect, useState } from "react";
import { applyAppearance, loadAppearance, saveAppearance, type Appearance } from "./appearance";

// The account menu's two device panels (#113): appearance, and diagnostics that show the build and the connection
// without any secret, token, cookie or upstream key.

export function AppearancePanel() {
  const [a, setA] = useState<Appearance>(loadAppearance);
  const change = (next: Appearance) => {
    setA(next);
    saveAppearance(next);
    applyAppearance(next);
  };
  return (
    <details className="settings">
      <summary>Appearance</summary>
      <p className="label">Saved on this device only.</p>
      <label>
        Theme{" "}
        <select value={a.theme} onChange={(e) => change({ ...a, theme: e.target.value as Appearance["theme"] })}>
          <option value="system">Follow the system</option>
          <option value="light">Light</option>
          <option value="dark">Dark</option>
        </select>
      </label>
      <label>
        Text size{" "}
        <select value={a.text} onChange={(e) => change({ ...a, text: e.target.value as Appearance["text"] })}>
          <option value="normal">Normal</option>
          <option value="large">Large</option>
          <option value="larger">Larger</option>
        </select>
      </label>
      <label>
        Conversation list width{" "}
        <select value={a.sidebar} onChange={(e) => change({ ...a, sidebar: e.target.value as Appearance["sidebar"] })}>
          <option value="narrow">Narrow</option>
          <option value="normal">Normal</option>
          <option value="wide">Wide</option>
        </select>
      </label>
      <label>
        <input type="checkbox" checked={a.focus} onChange={(e) => change({ ...a, focus: e.target.checked })} /> Focus mode (hide the conversation list on a wide screen)
      </label>
      <p className="label">Animations follow your system's reduced-motion setting.</p>
    </details>
  );
}

interface Diag {
  revision: string;
  bundle: string;
  online: boolean;
  ready: string;
  latencyMs: number | null;
  streams: boolean;
  worker: string;
}

/** What is running and whether this device can reach it. Reads only public, secret-free endpoints. */
export function DiagnosticsPanel() {
  const [d, setD] = useState<Diag | null>(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let live = true;
    void (async () => {
      const text = async (path: string) => {
        try {
          const r = await fetch(path, { credentials: "same-origin", cache: "no-store" });
          return r.ok ? await r.text() : "";
        } catch {
          return "";
        }
      };
      const began = performance.now();
      let ready = "unreachable";
      try {
        const r = await fetch("/readyz", { cache: "no-store" });
        ready = r.ok ? "ready" : `not ready (HTTP ${r.status})`;
      } catch {
        ready = "unreachable";
      }
      const latencyMs = ready === "unreachable" ? null : Math.round(performance.now() - began);
      let revision = "unknown";
      try {
        revision = (JSON.parse(await text("/api/version")) as { revision?: string }).revision || "unknown";
      } catch {
        revision = "unknown";
      }
      const bundle = (await text("/source-hash.txt")).trim().slice(0, 12) || "unknown";
      let worker = "not supported";
      if ("serviceWorker" in navigator) {
        const reg = await navigator.serviceWorker.getRegistration().catch(() => undefined);
        worker = !reg ? "not installed" : reg.waiting ? "an update is ready (reload to use it)" : reg.active ? "active, up to date" : "installing";
      }
      if (live) setD({ revision: revision.slice(0, 12), bundle, online: navigator.onLine, ready, latencyMs, streams: typeof EventSource !== "undefined", worker });
    })();
    return () => {
      live = false;
    };
  }, [tick]);
  return (
    <details className="settings">
      <summary>Diagnostics</summary>
      {!d ? (
        <p className="label">Checking…</p>
      ) : (
        <dl className="facts">
          <dt>Server build</dt>
          <dd>{d.revision}</dd>
          <dt>App bundle</dt>
          <dd>{d.bundle}</dd>
          <dt>Network</dt>
          <dd>{d.online ? "online" : "offline"}</dd>
          <dt>Server</dt>
          <dd>
            {d.ready}
            {d.latencyMs !== null ? `, answered in ${d.latencyMs} ms` : ""}
          </dd>
          <dt>Live replies</dt>
          <dd>{d.streams ? "supported" : "not supported by this browser"}</dd>
          <dt>App update</dt>
          <dd>{d.worker}</dd>
        </dl>
      )}
      <button type="button" className="quiet" onClick={() => setTick((n) => n + 1)}>
        Check again
      </button>
    </details>
  );
}

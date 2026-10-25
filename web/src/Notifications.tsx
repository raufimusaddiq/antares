import { useCallback, useEffect, useState } from "react";
import { disable, enable, loadNotifications, setMode, deviceStatus, unsupportedReason, type Device, type MissingReason, type Mode, type Notifications as State } from "./push";

const SAYS: Record<string, string> = {
  DENIED: "Notifications are blocked for this site in your browser settings.",
  DISMISSED: "Permission was not given, so nothing was turned on.",
  FEATURE_NOT_CONFIGURED: "Notifications are not set up on this server.",
  TOO_MANY_DEVICES: "Too many devices. Turn notifications off on one first.",
  NETWORK: "Could not reach Antares. Check your connection.",
};

const MISSING: Record<MissingReason, string> = {
  denied: "This browser has blocked notifications for this site, so alerts are not reaching it. Allow them in your browser settings, then turn them on here.",
  none: "This browser has no notification device registered, so alerts are not reaching it.",
  unlisted: "This browser has a subscription that Antares does not have on your account, so alerts are not reaching it.",
  unknown: "Antares could not confirm that this browser is registered for alerts, so they may not be reaching it.",
};

/** The per-device opt-in. Everything is off until the button is pressed. */
export function Notifications() {
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<State | null>(null);
  const [device, setDevice] = useState<Device | null>(null);
  const [missing, setMissing] = useState<MissingReason | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const unsupported = unsupportedReason();

  const refresh = useCallback(async () => {
    const r = await loadNotifications();
    if (!r.ok) {
      setLoadFailed(true);
      return;
    }
    setLoadFailed(false);
    setState(r.value);
    const s = await deviceStatus(r.value);
    setDevice(s.device);
    setMissing(s.missing);
  }, []);

  useEffect(() => {
    if (open) void refresh();
  }, [open, refresh]);

  const run = async (f: () => Promise<{ ok: boolean; code?: string }>) => {
    setBusy(true);
    setMsg("");
    const r = await f();
    if (!r.ok) setMsg(SAYS[r.code ?? ""] ?? "That did not work. Try again.");
    await refresh();
    setBusy(false);
  };

  return (
    <div className="notify">
      <button type="button" className="quiet" data-icon="bell" aria-expanded={open} onClick={() => setOpen(!open)}>
        Notifications
      </button>
      {open && (
        <section className="notify-panel" aria-label="Notifications">
          {unsupported ? (
            <p className="label">{unsupported}</p>
          ) : loadFailed ? (
            <p className="label">Could not load notification settings.</p>
          ) : !state ? (
            <p className="label">Loading…</p>
          ) : !state.configured ? (
            <p className="label">Notifications are not set up on this server.</p>
          ) : (
            <>
              <p className="label">Get a short alert on this device when a reply finishes or fails. It never includes what the chat was about.</p>
              {device ? (
                <>
                  <label>
                    Alert me about (applies to all your devices){" "}
                    <select value={state.mode} disabled={busy} onChange={(e) => void run(() => setMode(e.target.value as Mode))}>
                      <option value="all">Finished and failed replies</option>
                      <option value="failures">Failed replies only</option>
                      <option value="off">Nothing (muted)</option>
                    </select>
                  </label>
                  <p className="label">Turning off on this device removes only this device. Muting and the choice above apply to every device on your account.</p>
                  {state.mode === "off" && <p className="label">Alerts are muted for your account, on every device. Pick what to be alerted about to resume them.</p>}
                  <button type="button" disabled={busy} onClick={() => void run(() => disable(device))}>
                    Turn off on this device
                  </button>
                </>
              ) : (
                <>
                  {missing && (
                    <p className="notice" role="status">
                      {MISSING[missing]}
                    </p>
                  )}
                  <button type="button" disabled={busy} onClick={() => void run(enable)}>
                    Turn on for this device
                  </button>
                </>
              )}
            </>
          )}
          {msg && (
            <p className="notice" role="alert">
              {msg}
            </p>
          )}
        </section>
      )}
    </div>
  );
}

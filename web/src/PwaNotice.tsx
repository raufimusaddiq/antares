import { useEffect, useState } from "react";
import { reloadForUpdate, watchUpdates } from "./pwa";

export function UpdateNotice({ onReload }: { onReload: () => void }) {
  return <aside className="pwa-notice" aria-label="App update">
    <p role="status">An Antares update is ready. Reload when you are ready; this interrupts any reply in progress.</p>
    <button type="button" className="quiet" onClick={onReload}>Reload to update</button>
  </aside>;
}

export function PwaNotice() {
  const [waiting, setWaiting] = useState<ServiceWorker | null>(null);
  const [offline, setOffline] = useState(!navigator.onLine);
  useEffect(() => {
    let disposed = false;
    let stop = () => {};
    void watchUpdates(worker => { if (!disposed) setWaiting(worker); }).then(cleanup => {
      if (disposed) cleanup(); else stop = cleanup;
    }).catch(() => {});
    const online = () => setOffline(!navigator.onLine);
    window.addEventListener("online", online);
    window.addEventListener("offline", online);
    return () => {
      disposed = true;
      stop();
      window.removeEventListener("online", online);
      window.removeEventListener("offline", online);
    };
  }, []);
  return <>
    {offline && <aside className="pwa-notice"><p role="status">You are offline. Reconnect to continue chatting.</p></aside>}
    {waiting && <UpdateNotice onReload={() => reloadForUpdate(waiting, navigator.serviceWorker, () => window.location.reload())} />}
  </>;
}

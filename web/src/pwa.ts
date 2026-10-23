export function purgeUserState() {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  void navigator.serviceWorker.getRegistration().then(registration => {
    const workers = new Set([navigator.serviceWorker.controller, registration?.active, registration?.waiting, registration?.installing]);
    for (const worker of workers) worker?.postMessage({ type: "PURGE_USER_STATE" });
  }).catch(() => {});
}

export async function watchUpdates(onWaiting: (worker: ServiceWorker) => void): Promise<() => void> {
  if (!("serviceWorker" in navigator) || !import.meta.env.PROD) return () => {};
  const registration = await navigator.serviceWorker.register("/sw.js", { scope: "/", updateViaCache: "none" });
  const changed = () => {
    if (registration.waiting && navigator.serviceWorker.controller) onWaiting(registration.waiting);
  };
  const workers = new Set<ServiceWorker>();
  const installing = () => {
    if (registration.installing) {
      workers.add(registration.installing);
      registration.installing.addEventListener("statechange", changed);
    }
  };
  changed();
  installing();
  registration.addEventListener("updatefound", installing);
  return () => {
    registration.removeEventListener("updatefound", installing);
    for (const worker of workers) worker.removeEventListener("statechange", changed);
  };
}

export function reloadForUpdate(worker: ServiceWorker, container: ServiceWorkerContainer, reload: () => void) {
  if (worker.state === "activated" || worker.state === "redundant") { reload(); return; }
  container.addEventListener("controllerchange", reload, { once: true });
  worker.postMessage({ type: "ACTIVATE_UPDATE" });
}

// Opt-in Web Push for this browser (UXP-002). Nothing here runs until the reader presses
// "Turn on": no permission prompt, no service worker, no subscription by default.
// The server decides what a notification may say; this file only manages the device.

export type Mode = "off" | "all" | "failures";

export interface Device {
  id: string;
  endpointHash: string;
}

export interface Notifications {
  configured: boolean;
  mode: Mode;
  devices: Device[];
}

/** Why this browser cannot take notifications, or null when it can. */
export function unsupportedReason(env: { serviceWorker?: unknown; pushManager?: unknown; notification?: unknown; secure?: boolean } = browserEnv()): string | null {
  if (!env.secure) return "Notifications need a secure (https) connection.";
  if (!env.serviceWorker || !env.pushManager || !env.notification) return "This browser does not support push notifications.";
  return null;
}

function browserEnv() {
  return {
    serviceWorker: typeof navigator !== "undefined" && "serviceWorker" in navigator,
    pushManager: typeof window !== "undefined" && "PushManager" in window,
    notification: typeof window !== "undefined" && "Notification" in window,
    secure: typeof window !== "undefined" && window.isSecureContext === true,
  };
}

const CHAT_PATH = /^\/chat\/([0-9a-f]{32})\/?$/;

/** The chat a /chat/<id> address names, or null. The server still decides whether it is yours. */
export function chatIdFromPath(p: string): string | null {
  const m = CHAT_PATH.exec(p);
  return m ? m[1] : null;
}

export function b64urlToBytes(s: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const bin = atob((s + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function bytesToB64url(b: ArrayBuffer | null): string {
  if (!b) return "";
  let s = "";
  for (const c of new Uint8Array(b)) s += String.fromCharCode(c);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export async function sha256Hex(s: string): Promise<string> {
  const d = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s));
  return [...new Uint8Array(d)].map((x) => x.toString(16).padStart(2, "0")).join("");
}

type Result<T> = { ok: true; value: T } | { ok: false; code: string };

async function api<T>(method: string, path: string, body?: unknown): Promise<Result<T>> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    return { ok: false, code: "NETWORK" };
  }
  let json: unknown = null;
  try {
    json = await res.json();
  } catch {
    // 204 or not JSON
  }
  if (!res.ok) {
    const e = (json as { error?: { code?: unknown } } | null)?.error;
    return { ok: false, code: typeof e?.code === "string" ? e.code : "ERROR" };
  }
  return { ok: true, value: json as T };
}

export function parseNotifications(v: unknown): Notifications | null {
  if (typeof v !== "object" || v === null) return null;
  const o = v as Record<string, unknown>;
  if (typeof o.configured !== "boolean" || (o.mode !== "off" && o.mode !== "all" && o.mode !== "failures") || !Array.isArray(o.devices)) return null;
  const devices: Device[] = [];
  for (const d of o.devices) {
    const x = d as Record<string, unknown>;
    if (typeof x?.id === "string" && typeof x.endpointHash === "string") devices.push({ id: x.id, endpointHash: x.endpointHash });
  }
  return { configured: o.configured, mode: o.mode, devices };
}

export async function loadNotifications(): Promise<Result<Notifications>> {
  const r = await api<unknown>("GET", "/api/me/notifications");
  if (!r.ok) return r;
  const n = parseNotifications(r.value);
  return n ? { ok: true, value: n } : { ok: false, code: "ERROR" };
}

export async function setMode(mode: Mode): Promise<Result<Notifications>> {
  const r = await api<unknown>("PUT", "/api/me/notifications", { mode });
  if (!r.ok) return r;
  const n = parseNotifications(r.value);
  return n ? { ok: true, value: n } : { ok: false, code: "ERROR" };
}

async function currentSubscription(): Promise<PushSubscription | null> {
  const reg = await navigator.serviceWorker.getRegistration("/");
  return reg ? reg.pushManager.getSubscription() : null;
}

/** The server-side device row for this browser, if it has one. */
export async function thisDevice(n: Notifications): Promise<Device | null> {
  if (unsupportedReason() !== null) return null;
  try {
    const sub = await currentSubscription();
    if (!sub) return null;
    const h = await sha256Hex(sub.endpoint);
    return n.devices.find((d) => d.endpointHash === h) ?? null;
  } catch {
    return null;
  }
}

function save(sub: PushSubscription) {
  const json = sub.toJSON();
  return api<{ id: string }>("POST", "/api/push/subscriptions", { endpoint: sub.endpoint, keys: { p256dh: json.keys?.p256dh ?? bytesToB64url(sub.getKey("p256dh")), auth: json.keys?.auth ?? bytesToB64url(sub.getKey("auth")) } });
}

/** Must be called from a click: it is what asks for permission. */
export async function enable(): Promise<Result<true>> {
  if (unsupportedReason() !== null) return { ok: false, code: "UNSUPPORTED" };
  const perm = Notification.permission === "granted" ? "granted" : await Notification.requestPermission();
  if (perm !== "granted") return { ok: false, code: perm === "denied" ? "DENIED" : "DISMISSED" };
  const key = await api<{ publicKey?: string }>("GET", "/api/push/public-key");
  if (!key.ok) return key;
  if (typeof key.value.publicKey !== "string") return { ok: false, code: "ERROR" };
  let reg: ServiceWorkerRegistration;
  let sub: PushSubscription;
  const fresh = () => reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64urlToBytes(key.value.publicKey as string) });
  try {
    reg = await navigator.serviceWorker.register("/sw.js", { scope: "/", updateViaCache: "none" });
    await navigator.serviceWorker.ready;
    sub = (await reg.pushManager.getSubscription()) ?? (await fresh());
  } catch {
    return { ok: false, code: "SUBSCRIBE_FAILED" };
  }
  let saved = await save(sub);
  if (!saved.ok && saved.code === "ENDPOINT_IN_USE") {
    // This browser's subscription is registered to another account (a shared browser whose previous
    // user did not sign out cleanly). The server will not move it; drop it and take a fresh one.
    try {
      await sub.unsubscribe();
      sub = await fresh();
    } catch {
      return { ok: false, code: "SUBSCRIBE_FAILED" };
    }
    saved = await save(sub);
  }
  if (!saved.ok) {
    await sub.unsubscribe().catch(() => false); // never leave a subscription the server does not know
    return saved;
  }
  return { ok: true, value: true };
}

/** Revoke this device: the server stops first, so delivery ends even if the browser call fails. */
export async function disable(device: Device): Promise<Result<true>> {
  const r = await api<unknown>("DELETE", `/api/push/subscriptions/${encodeURIComponent(device.id)}`);
  if (!r.ok && r.code !== "NOT_FOUND") return r;
  try {
    await (await currentSubscription())?.unsubscribe();
  } catch {
    // the server row is gone; a leftover browser subscription receives nothing
  }
  return { ok: true, value: true };
}

/** Best effort at sign-out: a shared browser must not keep receiving the previous account's alerts. */
export async function revokeThisDevice(): Promise<void> {
  try {
    if (unsupportedReason() !== null) return;
    const n = await loadNotifications();
    if (!n.ok) return;
    const d = await thisDevice(n.value);
    if (d) await disable(d);
  } catch {
    // signing out must not depend on this
  }
}

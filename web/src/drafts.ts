// Unsent composer text. This file is the browser-local copy (localStorage, not encrypted); the
// server copy that follows the account across devices is DraftSync.tsx. Both are unencrypted. A key names the account and the chat,
// so one account never reads another's draft and one chat never shows another's.

const PREFIX = "antares:draft:v1:";

/** The draft of a chat that does not exist yet (the composer before anything was created). */
export const NEW_CHAT = null;

const keyOf = (email: string, chatId: string | null) => `${PREFIX}${encodeURIComponent(email)}:${chatId === NEW_CHAT ? "new" : `chat:${chatId}`}`;

export function loadDraft(email: string, chatId: string | null): string {
  try {
    return localStorage.getItem(keyOf(email, chatId)) ?? "";
  } catch {
    return "";
  }
}

// When each local draft was last written (#172). A draft with no stamp, or one older than DRAFT_TTL_MS, is
// purged when a session loads (purgeStaleDrafts): an expired session on a shared device leaves nothing behind
// for long, even if the browser was closed before the app could notice. The server copy restores it on sign-in.
export const DRAFT_TTL_MS = 7 * 24 * 60 * 60 * 1000;
const stampKey = (email: string, chatId: string | null) => `${PREFIX}at:${keyOf(email, chatId).slice(PREFIX.length)}`;

/** Empty text removes the entry rather than storing "". */
export function saveDraft(email: string, chatId: string | null, text: string, now = Date.now()): void {
  try {
    if (text === "") {
      localStorage.removeItem(keyOf(email, chatId));
      localStorage.removeItem(stampKey(email, chatId));
    } else {
      localStorage.setItem(keyOf(email, chatId), text);
      localStorage.setItem(stampKey(email, chatId), String(now));
    }
  } catch {
    // Storage unavailable or full: the draft just does not survive a reload.
  }
}

// The server revision this browser's copy was last in step with. Without it a later reopen cannot tell
// "my text is newer than the server's" from "another device sent or deleted the draft since"; with it,
// a deleted draft's tombstone is a conflict to resolve and not an empty slate to overwrite.
const baseKey = (email: string, chatId: string | null) => `${PREFIX}rev:${keyOf(email, chatId).slice(PREFIX.length)}`;

/** The synced revision, or null if this browser never synced this draft. */
export function loadBaseRevision(email: string, chatId: string | null): number | null {
  try {
    const v = localStorage.getItem(baseKey(email, chatId));
    return v !== null && /^\d+$/.test(v) ? Number(v) : null;
  } catch {
    return null;
  }
}

export function saveBaseRevision(email: string, chatId: string | null, rev: number): void {
  try {
    localStorage.setItem(baseKey(email, chatId), String(rev));
  } catch {
    // Without a baseline the next reopen reconciles conservatively (it asks).
  }
}

// "The box was emptied on purpose and the server has not been told yet" (a send or a clear whose DELETE
// failed). Without it a reload cannot tell an emptied box from a box that never had this draft, and would
// bring the server's old text back.
const clearedKey = (email: string, chatId: string | null) => `${PREFIX}cleared:${keyOf(email, chatId).slice(PREFIX.length)}`;

export function loadCleared(email: string, chatId: string | null): boolean {
  try {
    return localStorage.getItem(clearedKey(email, chatId)) === "1";
  } catch {
    return false;
  }
}

export function saveCleared(email: string, chatId: string | null, cleared: boolean): void {
  try {
    if (cleared) localStorage.setItem(clearedKey(email, chatId), "1");
    else localStorage.removeItem(clearedKey(email, chatId));
  } catch {
    // Unavailable: a reload then restores the server text rather than guessing that a delete was meant.
  }
}

/** A deleted chat leaves no unsent text, synced revision or cleared marker behind in this browser. */
export function forgetChatDrafts(email: string, chatId: string): void {
  try {
    for (const k of [keyOf(email, chatId), baseKey(email, chatId), clearedKey(email, chatId), stampKey(email, chatId)]) localStorage.removeItem(k);
  } catch {
    // Storage unavailable: there is nothing stored here to forget.
  }
}

/**
 * A session loaded for `email`: every other account's local drafts go, and so do this account's drafts that are
 * older than DRAFT_TTL_MS or carry no stamp, with their sync markers. One account never finds another's text on a
 * shared browser, and nothing outlives a week on the device.
 */
export function purgeStaleDrafts(email: string, now = Date.now()): void {
  const own = `${encodeURIComponent(email)}:`;
  try {
    const doomed: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (!k?.startsWith(PREFIX)) continue;
      const marker = /^(rev|cleared|at):/.exec(k.slice(PREFIX.length));
      const rest = k.slice(PREFIX.length + (marker ? marker[0].length : 0)); // "<email>:<chat>"
      if (!rest.startsWith(own)) {
        doomed.push(k); // another account's text or marker
      } else if (!marker) {
        const at = Number(localStorage.getItem(`${PREFIX}at:${rest}`));
        if (!(at > 0) || now - at > DRAFT_TTL_MS) doomed.push(k, `${PREFIX}at:${rest}`, `${PREFIX}rev:${rest}`, `${PREFIX}cleared:${rest}`);
      }
    }
    for (const k of doomed) localStorage.removeItem(k);
  } catch {
    // Storage unavailable: nothing is kept on this device to purge.
  }
}

/** Sign-out, an expired session and suspension: the next person on this browser inherits nothing. */
export function purgeDrafts(): void {
  try {
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const k = localStorage.key(i);
      if (k?.startsWith(PREFIX)) localStorage.removeItem(k);
    }
  } catch {
    // Nothing to purge when storage is unavailable.
  }
}

// ---- slash templates -----------------------------------------------------------------
// A fixed set of text insertions. They are not skills or tool calls; the reader
// edits the text before sending it.

export interface Template {
  name: string;
  body: string;
}

export const TEMPLATES: readonly Template[] = [
  { name: "summarize", body: "Summarize the following in five bullet points:\n\n" },
  { name: "explain", body: "Explain this in simple terms, with one example:\n\n" },
  { name: "review", body: "Review the following for mistakes and suggest improvements:\n\n" },
  { name: "translate", body: "Translate the following into English:\n\n" },
  { name: "steps", body: "Give me a step-by-step plan to:\n\n" },
];

/** The built-in set with the reader's own templates on top; an own template replaces a built-in of the same name. */
export function mergeTemplates(own: readonly Template[]): Template[] {
  const names = new Set(own.map((t) => t.name));
  return [...own, ...TEMPLATES.filter((t) => !names.has(t.name))].sort((a, b) => a.name.localeCompare(b.name));
}

/** Templates for a composer whose whole text is "/" plus a name prefix; none once the text has a space or newline. */
export function matchTemplates(text: string, list: readonly Template[] = TEMPLATES): Template[] {
  if (!/^\/[a-z0-9_-]*$/i.test(text)) return [];
  const q = text.slice(1).toLowerCase();
  return list.filter((t) => t.name.startsWith(q));
}

// Unsent composer text, kept in this browser's localStorage and nowhere else. It is
// never sent to the server and is not encrypted. A key names the account and the chat,
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

/** Empty text removes the entry rather than storing "". */
export function saveDraft(email: string, chatId: string | null, text: string): void {
  try {
    if (text === "") localStorage.removeItem(keyOf(email, chatId));
    else localStorage.setItem(keyOf(email, chatId), text);
  } catch {
    // Storage unavailable or full: the draft just does not survive a reload.
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

/** Templates for a composer whose whole text is "/" plus a name prefix; none once the text has a space or newline. */
export function matchTemplates(text: string): Template[] {
  if (!/^\/[a-z]*$/i.test(text)) return [];
  const q = text.slice(1).toLowerCase();
  return TEMPLATES.filter((t) => t.name.startsWith(q));
}

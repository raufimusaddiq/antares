// Chat export (PRD §21.8). Markdown is built in the browser from the owner's own
// history pages, one page at a time, and stops at the first refusal: if the chat
// stops being ours part-way (NOT_FOUND, sign-out) nothing partial is saved. Media
// is never fetched or embedded; a link in the text stays text.

import { describeCode, getMessages, type Turn } from "./chatApi";

const MAX_PAGES = 400;

export type Loaded = { ok: true; turns: Turn[] } | { ok: false; error: string };

/** Every turn of a chat, oldest first. */
export async function loadAllTurns(chatId: string, onProgress: (loaded: number, total: number) => void = () => {}): Promise<Loaded> {
  let cursor = "";
  let turns: Turn[] = [];
  for (let page = 0; page < MAX_PAGES; page++) {
    const r = await getMessages(chatId, cursor || undefined);
    if (!r.ok) return { ok: false, error: describeCode(r.code) };
    turns = [...r.value.turns, ...turns];
    onProgress(turns.length, r.value.total);
    if (r.value.nextCursor === "") return { ok: true, turns };
    cursor = r.value.nextCursor;
  }
  return { ok: false, error: "This conversation is too long to export." };
}

/** Turn text is kept verbatim, so the code fences, tables and image links the model wrote survive. */
export function toMarkdown(title: string, turns: Turn[], exportedAt: Date): string {
  const head = `# ${title.replace(/\s+/g, " ").trim() || "Antares chat"}\n\nExported ${exportedAt.toISOString().slice(0, 10)} from Antares.\n`;
  const body = turns.map((t) => `\n## ${t.role === "user" ? "You" : "Assistant"}\n\n${t.text.replace(/\r\n?/g, "\n").trim()}\n`);
  return head + body.join("");
}

export function fileName(title: string, ext: string): string {
  const slug = title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
  return `${slug || "antares-chat"}.${ext}`;
}

export function saveText(name: string, text: string, type = "text/markdown") {
  const url = URL.createObjectURL(new Blob([text], { type: `${type};charset=utf-8` }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

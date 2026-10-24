// Server drafts and templates (issue #107). The server owns the revision: a write names the
// revision it last saw (If-Match) and a stale one is refused with the winning text.

/** The server stores at most this many BYTES of draft (internal/store MaxDraftBytes); characters can be up to 4 bytes each. */
export const MAX_DRAFT_BYTES = 32768;
export const draftBytes = (text: string): number => new TextEncoder().encode(text).length;

/**
 * "error" = the server could not be reached or failed (worth retrying); "refused" = the server answered and said no,
 * which retrying the same text will not change. `reason` is shown to the reader.
 */
export type DraftWrite = { kind: "ok"; revision: number } | { kind: "conflict"; body: string; revision: number } | { kind: "error" } | { kind: "refused"; reason: string };
export type DraftRead = { kind: "ok"; body: string; revision: number } | { kind: "error" };

const draftPath = (chatId: string | null) => (chatId === null ? "/api/me/new-chat-draft" : `/api/chats/${encodeURIComponent(chatId)}/draft`);

async function send(method: string, path: string, ifMatch?: number, body?: unknown): Promise<{ status: number; json: unknown }> {
  try {
    const res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        ...(ifMatch === undefined ? {} : { "If-Match": `"${ifMatch}"` }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    let json: unknown = null;
    try {
      json = await res.json();
    } catch {
      // 204 or not JSON
    }
    return { status: res.status, json };
  } catch {
    return { status: 0, json: null };
  }
}

const rec = (v: unknown): Record<string, unknown> => (typeof v === "object" && v !== null ? (v as Record<string, unknown>) : {});

function parseDraft(v: unknown): { body: string; revision: number } | null {
  const r = rec(v);
  return typeof r.body === "string" && Number.isInteger(r.revision) ? { body: r.body, revision: r.revision as number } : null;
}

export async function readDraft(chatId: string | null): Promise<DraftRead> {
  const r = await send("GET", draftPath(chatId));
  const d = r.status === 200 ? parseDraft(r.json) : null;
  return d ? { kind: "ok", ...d } : { kind: "error" };
}

function written(r: { status: number; json: unknown }): DraftWrite {
  if (r.status === 409) {
    const cur = parseDraft(rec(r.json).current);
    return cur ? { kind: "conflict", ...cur } : { kind: "error" };
  }
  if (r.status === 0 || r.status >= 500 || r.status === 429) return { kind: "error" };
  if (r.status !== 200) return { kind: "refused", reason: refusalReason(r) };
  const d = parseDraft(r.json);
  return d ? { kind: "ok", revision: d.revision } : { kind: "error" };
}

/** The server's own words for a draft refusal (DRAFT_TOO_LARGE, DRAFT_INVALID_TEXT); any other refusal gets a fixed sentence. */
function refusalReason(r: { status: number; json: unknown }): string {
  const e = rec(rec(r.json).error);
  if (typeof e.code === "string" && e.code.startsWith("DRAFT_") && typeof e.message === "string") return e.message.slice(0, 200);
  return `The server did not accept this draft (HTTP ${r.status}).`;
}

export const writeDraft = async (chatId: string | null, body: string, rev: number) => written(await send("PUT", draftPath(chatId), rev, { body }));
export const removeDraft = async (chatId: string | null, rev: number) => written(await send("DELETE", draftPath(chatId), rev));

// ---- templates ---------------------------------------------------------------------

export interface OwnTemplate {
  id: string;
  name: string;
  body: string;
}

const parseTemplate = (v: unknown): OwnTemplate | null => {
  const r = rec(v);
  return typeof r.id === "string" && typeof r.name === "string" && typeof r.body === "string" ? { id: r.id, name: r.name, body: r.body } : null;
};

export async function listOwnTemplates(): Promise<OwnTemplate[] | null> {
  const r = await send("GET", "/api/me/templates");
  const list = rec(r.json).templates;
  if (r.status !== 200 || !Array.isArray(list)) return null;
  return list.map(parseTemplate).filter((t): t is OwnTemplate => t !== null);
}

/** `taken` = the name is already used; `invalid` = the server refused the name or body. */
export async function addTemplate(name: string, body: string): Promise<{ ok: true; value: OwnTemplate } | { ok: false; reason: "taken" | "invalid" | "failed" }> {
  const r = await send("POST", "/api/me/templates", undefined, { name, body });
  const t = r.status === 201 ? parseTemplate(r.json) : null;
  if (t) return { ok: true, value: t };
  return { ok: false, reason: r.status === 409 ? "taken" : r.status === 422 ? "invalid" : "failed" };
}

/** Change a template in place. `gone` = it no longer exists (deleted on another device). */
export async function saveTemplate(id: string, name: string, body: string): Promise<{ ok: true; value: OwnTemplate } | { ok: false; reason: "taken" | "invalid" | "gone" | "failed" }> {
  const r = await send("PUT", `/api/me/templates/${encodeURIComponent(id)}`, undefined, { name, body });
  const t = r.status === 200 ? parseTemplate(r.json) : null;
  if (t) return { ok: true, value: t };
  return { ok: false, reason: r.status === 409 ? "taken" : r.status === 422 ? "invalid" : r.status === 404 ? "gone" : "failed" };
}

export async function removeTemplate(id: string): Promise<boolean> {
  return (await send("DELETE", `/api/me/templates/${encodeURIComponent(id)}`)).status === 204;
}

// ---- share target --------------------------------------------------------------------

/**
 * Text the OS shared to the installed app (manifest share_target, GET): title, text and url
 * joined. Null when the page was not opened by a share. It is only ever offered as draft text.
 */
export function sharedText(search: string): string | null {
  const q = new URLSearchParams(search);
  if (q.get("share") !== "1") return null;
  const parts = [q.get("title"), q.get("text"), q.get("url")].map((s) => (s ?? "").trim()).filter(Boolean);
  const joined = parts.filter((p, i) => parts.indexOf(p) === i).join("\n");
  return joined === "" ? null : joined.slice(0, 8000);
}

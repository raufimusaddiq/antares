// The browser's side of image input and media preview (PRD §27.3 MEDIA-EXT, issue #101).
// It sends cookies, the user's own file bytes and asset ids the server issued to that user:
// never a profile, model, route, key or session id. Whether an image may be sent at all is the
// server's answer (GET /api/chats/{id}/input, from the Kenari catalogue), never a guess here.

import { describeCode, type ApiResult } from "./chatApi";

export const IMAGE_TYPES: readonly string[] = ["image/png", "image/jpeg", "image/gif"];
export const MAX_IMAGE_BYTES = 8 << 20;
export const MAX_IMAGES = 4;
/** Together per message; the server keeps the encoded request under the upstream's 10 MB cap. */
export const MAX_TURN_BYTES = 6 << 20;

export type PrecheckFailure = "UNSUPPORTED_TYPE" | "TOO_LARGE";

/** A fast local refusal. The server validates the bytes again (magic, pixels, ownership). */
export function precheckImage(file: { type: string; size: number }): PrecheckFailure | null {
  if (!IMAGE_TYPES.includes(file.type)) return "UNSUPPORTED_TYPE";
  if (file.size <= 0 || file.size > MAX_IMAGE_BYTES) return "TOO_LARGE";
  return null;
}

export function describePrecheck(f: PrecheckFailure): string {
  return f === "UNSUPPORTED_TYPE" ? "Only PNG, JPEG and GIF images can be attached." : "That image is larger than 8 MiB.";
}

/** Image files from a paste or drop. Anything that is not an image is ignored, not uploaded. */
export function imageFilesFrom(dt: Pick<DataTransfer, "files" | "items"> | null | undefined): File[] {
  if (!dt) return [];
  const out: File[] = [];
  if (dt.files && dt.files.length > 0) {
    for (const f of Array.from(dt.files)) if (f.type.startsWith("image/")) out.push(f);
    return out;
  }
  for (const it of Array.from(dt.items ?? [])) {
    if (it.kind === "file" && it.type.startsWith("image/")) {
      const f = it.getAsFile();
      if (f) out.push(f);
    }
  }
  return out;
}

export const assetUrl = (id: string, inline = true) => `/api/assets/${encodeURIComponent(id)}${inline ? "?inline=1" : ""}`;

export interface ImageInput {
  available: boolean;
  /** Why not: a stable server code, shown through describeImageReason. */
  reason: string | null;
  model: string | null;
}

export function parseImageInput(v: unknown): ImageInput | null {
  if (typeof v !== "object" || v === null) return null;
  const i = (v as Record<string, unknown>).image;
  if (typeof i !== "object" || i === null) return null;
  const r = i as Record<string, unknown>;
  if (typeof r.available !== "boolean") return null;
  return { available: r.available, reason: typeof r.reason === "string" ? r.reason : null, model: typeof r.model === "string" ? r.model : null };
}

export function describeImageReason(reason: string | null): string {
  switch (reason) {
    case "MODEL_TEXT_ONLY":
      return "The model for this chat does not accept images. Switch to a model that does.";
    case "MODEL_NOT_IN_CATALOGUE":
      return "Image support for this chat's model is unknown, so images are off.";
    case "CATALOGUE_UNAVAILABLE":
    case "CATALOGUE_NOT_CONFIGURED":
      return "Image support cannot be confirmed right now, so images are off.";
    case "NO_CHAT":
      return "Open or start a chat first, then attach images.";
    default:
      return "Images cannot be sent in this chat.";
  }
}

async function json(res: Response): Promise<unknown> {
  try {
    return await res.json();
  } catch {
    return null;
  }
}

function errorCode(body: unknown): string | null {
  const e = (body as { error?: { code?: unknown } } | null)?.error;
  return typeof e?.code === "string" ? e.code : null;
}

export async function getImageInput(chatId: string, signal?: AbortSignal): Promise<ApiResult<ImageInput>> {
  try {
    const res = await fetch(`/api/chats/${encodeURIComponent(chatId)}/input`, { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
    const body = await json(res);
    const v = res.ok ? parseImageInput(body) : null;
    if (v) return { ok: true, value: v };
    return { ok: false, code: errorCode(body) ?? "HERMES_UNAVAILABLE", status: res.status };
  } catch {
    return { ok: false, code: "NETWORK", status: 0 };
  }
}

/** Stages one image as the caller's own asset, with progress. The id is the only thing later sent. */
export function uploadImage(file: File, onProgress: (fraction: number) => void, signal?: AbortSignal): Promise<ApiResult<{ id: string }>> {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    const fail = (code: string, status = 0) => resolve({ ok: false, code, status });
    xhr.open("POST", "/api/assets");
    xhr.setRequestHeader("Content-Type", file.type);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(Math.min(1, e.loaded / e.total));
    };
    xhr.onerror = () => fail("NETWORK");
    xhr.onabort = () => fail("CANCELLED");
    xhr.onload = () => {
      let body: unknown = null;
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        // not JSON: handled below
      }
      const id = (body as { id?: unknown } | null)?.id;
      if (xhr.status === 201 && typeof id === "string") resolve({ ok: true, value: { id } });
      else fail(errorCode(body) ?? "HERMES_UNAVAILABLE", xhr.status);
    };
    signal?.addEventListener("abort", () => xhr.abort(), { once: true });
    xhr.send(file);
  });
}

export async function deleteAsset(id: string): Promise<boolean> {
  try {
    const res = await fetch(`/api/assets/${encodeURIComponent(id)}`, { method: "DELETE", credentials: "same-origin" });
    return res.status === 204 || res.status === 404;
  } catch {
    return false;
  }
}

/** POST /api/chats/{id}/messages with staged images. Same request as a text send, plus the ids. */
export async function sendMessageWithImages(chatId: string, input: string, key: string, imageAssetIds: string[]): Promise<ApiResult<{ runId: string }>> {
  try {
    const res = await fetch(`/api/chats/${encodeURIComponent(chatId)}/messages`, {
      method: "POST",
      credentials: "same-origin",
      headers: { Accept: "application/json", "Content-Type": "application/json", "Idempotency-Key": key },
      body: JSON.stringify({ input, imageAssetIds }),
    });
    const body = await json(res);
    const runId = (body as { runId?: unknown } | null)?.runId;
    if (res.ok && typeof runId === "string") return { ok: true, value: { runId } };
    return { ok: false, code: errorCode(body) ?? "HERMES_UNAVAILABLE", status: res.status };
  } catch {
    return { ok: false, code: "NETWORK", status: 0 };
  }
}

/** The message for a refused or failed image send, falling back to the shared vocabulary. */
export function describeImageSend(code: string): string {
  if (code === "IMAGE_INPUT_UNAVAILABLE") return "This chat's model cannot take images. Your message was not sent.";
  if (code === "PAYLOAD_TOO_LARGE") return "The images are too large to send together. Remove one and try again.";
  return describeCode(code);
}

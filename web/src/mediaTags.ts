// The agent's media tools reply with `MEDIA:<path>`, where <path> is a file in the user's own assistant profile cache
// (an image it generated, a voice reply, a video). The runs route Antares uses returns the tag untouched, so the tag is
// taken out of the text (no server path is shown) and its path is kept for GeneratedMedia, which loads the file from
// /api/media: the server serves only the signed-in user's own profile.

const TAG = /MEDIA:(?:"([^"\n]*)"|'([^'\n]*)'|(\S+))/g;
const VOICE_MARKER = /\[\[audio_as_voice\]\]\s*/g;

export interface SplitMedia {
  text: string;
  /** The media paths, in order, without duplicates. */
  paths: string[];
}

export function splitMedia(text: string): SplitMedia {
  const paths: string[] = [];
  const out = text.replace(VOICE_MARKER, "").replace(TAG, (_m, dq?: string, sq?: string, bare?: string) => {
    const p = (dq ?? sq ?? (bare ?? "").replace(/[.,;:!?)\]}>]+$/, "")).trim();
    if (p && !paths.includes(p)) paths.push(p);
    return "";
  });
  return { text: out.replace(/\n{3,}/g, "\n\n").trim(), paths };
}

export type MediaKind = "image" | "audio" | "video" | null;

export function mediaKind(path: string): MediaKind {
  const ext = path.slice(path.lastIndexOf(".") + 1).toLowerCase();
  if (["png", "jpg", "jpeg", "webp", "gif"].includes(ext)) return "image";
  if (["mp3", "ogg", "wav", "m4a"].includes(ext)) return "audio";
  if (["mp4", "webm"].includes(ext)) return "video";
  return null;
}

export const mediaUrl = (path: string) => `/api/media?path=${encodeURIComponent(path)}`;

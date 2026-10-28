// The agent's media tools reply with `MEDIA:<…>` naming a file they generated (an image, a voice reply, a video). In
// a turn read from history the server has already replaced the file's location with an opaque id, `<kind>-<sealed>`,
// that only the signed-in user can open; the tag is taken out of the text and the id is kept for GeneratedMedia.

// An unclosed quote (a truncated reply) runs to the end of its line, as on the server.
const TAG = /MEDIA:(?:"([^"\n]*)"?|'([^'\n]*)'?|(\S+))/g;
const VOICE_MARKER = /\[\[audio_as_voice\]\]\s*/g;

export interface SplitMedia {
  text: string;
  /** The media ids, in order, without duplicates. */
  ids: string[];
}

/** Removes the tags (and the voice marker) and nothing else from the reply. */
export function splitMedia(text: string): SplitMedia {
  const ids: string[] = [];
  const out = text.replace(VOICE_MARKER, "").replace(TAG, (_m, dq?: string, sq?: string, bare?: string) => {
    // An id is <kind>-<base64url>; punctuation after it (a full stop ending the sentence) stays in the text.
    const id = dq ?? sq ?? (bare ?? "").match(/^[a-z]+-[A-Za-z0-9_-]+/)?.[0] ?? bare ?? "";
    if (id && !ids.includes(id)) ids.push(id);
    return (bare ?? "").slice(id.length);
  });
  return { text: out.trimEnd(), ids };
}

export type MediaKind = "image" | "audio" | "video" | null;

/** The kind the server put in front of the id; anything else (a raw path in a live reply) is no media id at all. */
export function mediaKind(id: string): MediaKind {
  const kind = id.slice(0, id.indexOf("-"));
  return kind === "image" || kind === "audio" || kind === "video" ? kind : null;
}

export const mediaUrl = (id: string) => `/api/media?id=${encodeURIComponent(id)}`;

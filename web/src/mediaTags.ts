// The agent's media tools reply with `MEDIA:<path>` where <path> is a file on the agent's own
// host. Only the chat-completions and responses routes turn that into bytes; the runs route
// Antares uses returns the tag untouched (docs/runbooks/media.md). Antares cannot read those
// files and must not show a server path (MEDIA-EXT-006), so the tag is replaced with a plain
// statement and the path is dropped. Nothing is fetched, linked or guessed from it.

const TAG = /MEDIA:(?:"[^"\n]*"|'[^'\n]*'|\S+)/g;
const VOICE_MARKER = /\[\[audio_as_voice\]\]\s*/g;

export const MEDIA_UNAVAILABLE = "[Generated media was produced but is not available here]";

export interface SanitizedMedia {
  text: string;
  /** How many tags were replaced. */
  count: number;
}

export function hideMediaPaths(text: string): SanitizedMedia {
  let count = 0;
  const out = text.replace(VOICE_MARKER, "").replace(TAG, () => {
    count++;
    return MEDIA_UNAVAILABLE;
  });
  return { text: out, count };
}

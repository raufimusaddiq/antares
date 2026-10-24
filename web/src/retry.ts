import type { Turn } from "./chatApi";
import type { AttachmentRef } from "./MediaPreview";

// "Try again" re-asks the chat's last user turn. The text and the images are both read from that
// one turn of the chat on screen, so a reload or a switch of chat cannot pair this chat's text with
// another chat's (or no) images. A turn whose images cannot be proven (never matched to a stored
// image, a placeholder in its text, an image since deleted) is not re-sent without them: the text
// goes back into the box and the reader attaches the images again.

export type RetryPlan =
  | { kind: "send"; text: string; images: AttachmentRef[] }
  | { kind: "redraft"; text: string; why: string }
  | { kind: "none" };

const IMAGE_MARK = /\[image\]/;

export function planRetry(turns: readonly Turn[]): RetryPlan {
  const last = [...turns].reverse().find((t) => t.role === "user");
  if (!last || last.text.trim() === "") return { kind: "none" };
  const atts = last.attachments ?? [];
  if (atts.length > 0) {
    if (atts.every((a) => a.available)) return { kind: "send", text: last.text, images: atts };
    return { kind: "redraft", text: last.text, why: "Some images sent with that message are no longer available. Its text is back in the box; attach the images again." };
  }
  if (IMAGE_MARK.test(last.text)) {
    return { kind: "redraft", text: last.text, why: "That message had images that could not be matched to it. Its text is back in the box; attach the images again." };
  }
  return { kind: "send", text: last.text, images: [] };
}

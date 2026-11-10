// Where a post can be sent. Every destination carries the post's title and its one canonical address and nothing else: no
// tracking parameter, no identifier of the sharer (spec 002 FR-003). The destinations are plain links the reader follows; no
// script of any of these services is loaded (FR-018).

/** The canonical address of a published post: the origin and /b/{slug}, with no query and no fragment. */
export function canonicalUrl(origin: string, slug: string): string {
  return `${origin.replace(/\/+$/, "")}/b/${slug}`;
}

export type Destination = { id: "whatsapp" | "x" | "facebook" | "linkedin" | "telegram" | "email"; label: string; href: string };

const q = encodeURIComponent;

export function destinations(title: string, url: string): Destination[] {
  return [
    { id: "whatsapp", label: "WhatsApp", href: `https://wa.me/?text=${q(`${title} ${url}`)}` },
    { id: "x", label: "X", href: `https://twitter.com/intent/tweet?text=${q(title)}&url=${q(url)}` },
    { id: "facebook", label: "Facebook", href: `https://www.facebook.com/sharer/sharer.php?u=${q(url)}` },
    { id: "linkedin", label: "LinkedIn", href: `https://www.linkedin.com/sharing/share-offsite/?url=${q(url)}` },
    { id: "telegram", label: "Telegram", href: `https://t.me/share/url?url=${q(url)}&text=${q(title)}` },
    { id: "email", label: "Email", href: `mailto:?subject=${q(title)}&body=${q(url)}` },
  ];
}

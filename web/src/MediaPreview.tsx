import { useEffect, useRef, useState } from "react";
import "./media.css";
import { assetUrl } from "./mediaApi";

// Safe previews of media the signed-in user owns (MEDIA-EXT-003/005/007). Every source is
// /api/assets/{id}?inline=1, which the server serves only to the owner, as a validated raster
// image or audio, with nosniff and a sandbox policy. Nothing here takes a URL from message
// text: callers pass an asset id the server issued. Bytes load on demand (lazy images,
// audio preload="none"), never eagerly.

export interface AttachmentRef {
  assetId: string;
  mediaType: string;
  available: boolean;
}

export function Lightbox({ src, alt, onClose }: { src: string; alt: string; onClose: () => void }) {
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    close.current?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("keydown", key);
      prev?.focus?.();
    };
  }, [onClose]);
  return (
    <div role="dialog" aria-modal="true" aria-label={alt} data-media="lightbox" onClick={onClose}>
      <img src={src} alt={alt} onClick={(e) => e.stopPropagation()} />
      <a href={src.replace("?inline=1", "")} download onClick={(e) => e.stopPropagation()}>
        Download
      </a>
      <button ref={close} type="button" onClick={onClose}>
        Close
      </button>
    </div>
  );
}

/** One image the user sent, in history. A deleted asset says so instead of a broken picture. */
export function SentImage({ att, index }: { att: AttachmentRef; index: number }) {
  const [open, setOpen] = useState(false);
  const [broken, setBroken] = useState(false);
  const label = `Image ${index + 1} you sent`;
  if (!att.available || broken) return <span data-media="gone">{label} (no longer available)</span>;
  const src = assetUrl(att.assetId);
  return (
    <>
      <button type="button" data-media="thumbbtn" aria-label={`Open ${label}`} onClick={() => setOpen(true)}>
        <img src={src} alt={label} loading="lazy" decoding="async" data-media="thumb" onError={() => setBroken(true)} />
      </button>
      {open && <Lightbox src={src} alt={label} onClose={() => setOpen(false)} />}
    </>
  );
}

/** Owned audio: native controls give play/pause, seek (the server answers Range) and duration. */
export function OwnedAudio({ assetId, label }: { assetId: string; label: string }) {
  const [failed, setFailed] = useState(false);
  if (failed) return <span data-media="gone">{label} (no longer available)</span>;
  return (
    <figure data-media="audio">
      <figcaption>{label}</figcaption>
      <audio controls preload="none" src={assetUrl(assetId)} onError={() => setFailed(true)} />
      <a href={assetUrl(assetId, false)} download>
        Download
      </a>
    </figure>
  );
}

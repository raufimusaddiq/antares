import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { saveBlob } from "../../exportChat";
import { renderPostCard, type PostCardData } from "./postCard";

type Made = { url: string; file: File };

/**
 * A post as a story picture to send or save (spec 002, the "Share as a story" frames). The picture is drawn when the dialog
 * opens, so the Send tap that follows is a direct user action (iOS Safari refuses to share otherwise). Where the device can
 * hand a picture to an app it does; elsewhere the picture is saved, the link is copied and the person is told what to do next.
 */
export function StoryDialog({ data, url, slug, onClose }: { data: PostCardData; url: string; slug: string; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [made, setMade] = useState<Made | "failed" | null>(null);
  const [note, setNote] = useState("");
  const [tick, setTick] = useState(0);
  const latest = useRef(data);
  latest.current = data;
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) {
      if (typeof d.showModal === "function") d.showModal();
      else d.setAttribute("open", "");
    }
  }, []);
  useEffect(() => {
    setMade(null);
    let blobUrl = "";
    let live = true;
    void renderPostCard(latest.current).then((blob) => {
      if (!live) return;
      if (!blob) return setMade("failed");
      blobUrl = URL.createObjectURL(blob);
      setMade({ url: blobUrl, file: new File([blob], `antares-${slug}.png`, { type: "image/png" }) });
    }, () => live && setMade("failed"));
    return () => {
      live = false;
      if (blobUrl) URL.revokeObjectURL(blobUrl);
    };
  }, [slug, tick]);

  const ready = made !== null && made !== "failed" ? made : null;
  const canSend = ready !== null && typeof navigator.canShare === "function" && navigator.canShare({ files: [ready.file] });
  const copy = useCallback(async (then?: string) => {
    try {
      await navigator.clipboard.writeText(url);
      setNote(then ?? "Link copied.");
      return true;
    } catch {
      setNote(`Copy this address by hand: ${url}`);
      return false;
    }
  }, [url]);
  const send = () => {
    if (!ready) return;
    navigator.share({ files: [ready.file], title: data.title, url }).catch((e: unknown) => {
      if (!(e instanceof DOMException && e.name === "AbortError")) setNote("Could not send it from here. Save the image instead.");
    });
  };
  const save = () => {
    if (!ready) return;
    saveBlob(ready.file.name, ready.file);
    void copy("Saved to your device and the link is copied. In Instagram, add the picture to your story, then add a link sticker and paste the link.");
  };

  return createPortal(
    <dialog ref={ref} className="settings-dialog share-image" aria-labelledby="story-title" onClose={onClose} onCancel={onClose}>
      <header>
        <h2 id="story-title">Share as a story</h2>
        <button type="button" className="quiet icon-only" data-icon="close" aria-label="Close" onClick={onClose} />
      </header>
      <div className="share-image-body">
        {made === null && (
          <p className="label" role="status">
            Making your picture…
          </p>
        )}
        {made === "failed" && (
          <p role="alert">Could not make the picture on this device. The post is unaffected and you can still copy its link.</p>
        )}
        {ready && <img src={ready.url} alt={`The story picture: ${data.title}. ${data.summary}`} />}
        {ready && !note && <p className="label">A 9:16 picture of this post: its title, summary and highlights. Nothing is uploaded.</p>}
        <div className="actions">
          {canSend && (
            <button type="button" data-icon="share" onClick={send}>
              Share to an app
            </button>
          )}
          {made === "failed" ? (
            <button type="button" className="quiet" onClick={() => setTick((t) => t + 1)}>
              Try again
            </button>
          ) : (
            <button type="button" className={canSend ? "quiet" : undefined} data-icon="download" disabled={!ready} onClick={save}>
              Save image
            </button>
          )}
          <button type="button" className="quiet" onClick={() => void copy()}>
            Copy link
          </button>
        </div>
        {note && <p role="status">{note}</p>}
      </div>
    </dialog>,
    document.body,
  );
}

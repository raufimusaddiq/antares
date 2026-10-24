import { useCallback, useEffect, useRef, useState } from "react";
import "./media.css";
import { MAX_IMAGES, MAX_TURN_BYTES, deleteAsset, describePrecheck, precheckImage, uploadImage } from "./mediaApi";
import { describeCode } from "./chatApi";

// Composer-side state for images being attached (MEDIA-EXT-001): file name, type, size, thumbnail,
// progress, retry and delete, each tied to the server-issued asset id once the upload succeeds.
// The hook and strip hold no layout of the composer; the composer owns where they sit and when
// a send is allowed (an attachment that is not "ready" blocks the send, it is never dropped).

export interface Attachment {
  key: string;
  file: File;
  previewUrl: string;
  status: "uploading" | "ready" | "failed";
  progress: number;
  assetId?: string;
  error?: string;
}

let seq = 0;

export function useImageAttachments() {
  const [items, setItems] = useState<Attachment[]>([]);
  const [notice, setNotice] = useState<string | null>(null);
  const live = useRef(new Map<string, AbortController>());
  const itemsRef = useRef<Attachment[]>([]);
  itemsRef.current = items;

  const patch = useCallback((key: string, p: Partial<Attachment>) => setItems((cur) => cur.map((a) => (a.key === key ? { ...a, ...p } : a))), []);

  const start = useCallback(
    async (a: Attachment) => {
      const ctl = new AbortController();
      live.current.set(a.key, ctl);
      patch(a.key, { status: "uploading", progress: 0, error: undefined });
      const r = await uploadImage(a.file, (p) => patch(a.key, { progress: p }), ctl.signal);
      live.current.delete(a.key);
      if (r.ok) patch(a.key, { status: "ready", progress: 1, assetId: r.value.id });
      else if (r.code !== "CANCELLED") patch(a.key, { status: "failed", error: describeCode(r.code) });
    },
    [patch],
  );

  const add = useCallback(
    (files: File[]) => {
      let count = itemsRef.current.length;
      let bytes = itemsRef.current.reduce((n, a) => n + a.file.size, 0);
      const refusals: string[] = [];
      for (const file of files) {
        const bad = precheckImage(file);
        if (bad) refusals.push(`${file.name || "image"}: ${describePrecheck(bad)}`);
        else if (count >= MAX_IMAGES) refusals.push(`At most ${MAX_IMAGES} images per message.`);
        else if (bytes + file.size > MAX_TURN_BYTES) refusals.push("The images together are over 6 MiB.");
        else {
          count++;
          bytes += file.size;
          const a: Attachment = { key: `att${++seq}`, file, previewUrl: URL.createObjectURL(file), status: "uploading", progress: 0 };
          setItems((cur) => [...cur, a]);
          void start(a);
        }
      }
      setNotice(refusals.length ? [...new Set(refusals)].join(" ") : null);
    },
    [start],
  );

  const drop = useCallback((a: Attachment, deleteStaged: boolean) => {
    live.current.get(a.key)?.abort();
    live.current.delete(a.key);
    URL.revokeObjectURL(a.previewUrl);
    if (deleteStaged && a.assetId) void deleteAsset(a.assetId);
  }, []);

  const remove = useCallback(
    (key: string) => {
      const a = itemsRef.current.find((x) => x.key === key);
      if (a) drop(a, true);
      setItems((cur) => cur.filter((x) => x.key !== key));
    },
    [drop],
  );

  const retry = useCallback(
    (key: string) => {
      const a = itemsRef.current.find((x) => x.key === key);
      if (a && a.status === "failed") void start(a);
    },
    [start],
  );

  /** After a successful send: forget the strip, keep the staged assets (history shows them). */
  const release = useCallback(() => {
    itemsRef.current.forEach((a) => drop(a, false));
    setItems([]);
    setNotice(null);
  }, [drop]);

  /** The reader left the message: forget the strip and delete what was staged for it. */
  const discard = useCallback(() => {
    if (itemsRef.current.length === 0) return;
    itemsRef.current.forEach((a) => drop(a, true));
    setItems([]);
    setNotice(null);
  }, [drop]);

  useEffect(() => () => itemsRef.current.forEach((a) => drop(a, false)), [drop]);

  const readyIds = items.filter((a) => a.status === "ready" && a.assetId).map((a) => a.assetId as string);
  const pending = items.some((a) => a.status !== "ready");
  return { items, notice, add, remove, retry, release, discard, readyIds, /** true while any image is uploading or failed: do not send */ pending };
}

function kb(n: number) {
  return n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MiB` : `${Math.max(1, Math.round(n / 1024))} KiB`;
}

export function ImageStrip({ items, onRemove, onRetry }: { items: Attachment[]; onRemove: (key: string) => void; onRetry: (key: string) => void }) {
  if (items.length === 0) return null;
  return (
    <ul data-media="strip" aria-label="Attached images">
      {items.map((a) => (
        <li key={a.key} data-media="item" data-status={a.status}>
          <img src={a.previewUrl} alt={a.file.name || "Attached image"} data-media="thumb" />
          <span data-media="meta">
            <span data-media="name">{a.file.name || "image"}</span>
            <span>
              {a.file.type.replace("image/", "").toUpperCase()} · {kb(a.file.size)}
            </span>
            {a.status === "uploading" && <progress max={1} value={a.progress} aria-label={`Uploading ${a.file.name || "image"}`} />}
            {a.status === "failed" && <span role="alert">{a.error ?? "Upload failed"}</span>}
          </span>
          {a.status === "failed" && (
            <button type="button" onClick={() => onRetry(a.key)}>
              Retry
            </button>
          )}
          <button type="button" aria-label={`Remove ${a.file.name || "image"}`} onClick={() => onRemove(a.key)}>
            Remove
          </button>
        </li>
      ))}
    </ul>
  );
}

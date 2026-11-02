import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { createShare, describeCode, getShare, shareUrl, stopShare, type ApiResult } from "./chatApi";
import { saveBlob } from "./exportChat";
import { renderShareImage } from "./shareImage";

type LinkState =
  | { kind: "loading" }
  | { kind: "none" }
  | { kind: "shared"; createdAt: string; turns: number; url?: string }
  | { kind: "error"; message: string };

function shareError(r: { code: string; status: number }): string {
  if (r.status === 409 && r.code === "INVALID_REQUEST") return "There is nothing in this chat to share yet.";
  if (r.status === 413) return "This conversation is too long to share by link. Export it instead.";
  return describeCode(r.code);
}

const day = (iso: string) => iso.slice(0, 10);

/**
 * A read-only link to the open chat (#250). The link holds a copy made now; the server keeps only its hash, so the
 * address is shown when it is made and a lost one is replaced, not recovered.
 */
export function ShareChat({ chatId }: { chatId: string }) {
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<LinkState>({ kind: "loading" });
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const current = useRef(chatId);
  useEffect(() => {
    current.current = chatId;
    setOpen(false);
    setNote("");
    setState({ kind: "loading" });
  }, [chatId]);
  useEffect(() => {
    if (!open) return;
    const id = chatId;
    void getShare(id).then((r) => {
      if (current.current !== id) return;
      setState(!r.ok ? { kind: "error", message: describeCode(r.code) } : r.value.shared ? { kind: "shared", createdAt: r.value.createdAt, turns: r.value.turns } : { kind: "none" });
    });
  }, [open, chatId]);

  const make = async () => {
    const id = chatId;
    setBusy(true);
    setNote("");
    const r = await createShare(id);
    if (current.current !== id) return;
    setBusy(false);
    if (!r.ok) return setNote(shareError(r));
    setState({ kind: "shared", createdAt: r.value.createdAt, turns: r.value.turns, url: shareUrl(r.value.token) });
  };
  const stop = async () => {
    const id = chatId;
    setBusy(true);
    setNote("");
    const r = await stopShare(id);
    if (current.current !== id) return;
    setBusy(false);
    if (!r.ok) return setNote(describeCode(r.code));
    setState({ kind: "none" });
    setNote("Sharing stopped. The link no longer opens.");
  };
  const copy = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url);
      setNote("Link copied.");
    } catch {
      setNote("Could not copy. Select the link and copy it.");
    }
  };

  return (
    <div className="share">
      <button type="button" className="quiet" data-icon="share" aria-expanded={open} aria-controls="share-panel" onClick={() => setOpen((o) => !o)}>
        Share link
      </button>
      {open && (
        <div id="share-panel" className="share-panel">
          <p className="label">Anyone with the link can read this chat as it is now, without signing in. Later messages are not added. Images and files are left out.</p>
          {state.kind === "loading" && <p className="label" role="status">Checking…</p>}
          {state.kind === "error" && <p role="alert">{state.message}</p>}
          {state.kind === "none" && (
            <button type="button" disabled={busy} onClick={() => void make()}>
              Create link
            </button>
          )}
          {state.kind === "shared" && (
            <>
              {state.url ? (
                <div className="share-link">
                  <input readOnly aria-label="Share link" value={state.url} onFocus={(e) => e.currentTarget.select()} />
                  <button type="button" className="quiet" data-icon="copy" onClick={() => void copy(state.url!)}>
                    Copy
                  </button>
                  {typeof navigator.share === "function" && (
                    <button type="button" className="quiet" data-icon="share" onClick={() => void navigator.share({ url: state.url }).catch(() => {})}>
                      Send…
                    </button>
                  )}
                </div>
              ) : (
                <p className="label">
                  Shared on {day(state.createdAt)} ({state.turns} messages). The link was shown when it was made; make a new one to get it again.
                </p>
              )}
              <div className="actions">
                {!state.url && (
                  <button type="button" className="quiet" disabled={busy} onClick={() => void make()}>
                    New link
                  </button>
                )}
                <button type="button" className="quiet" disabled={busy} onClick={() => void stop()}>
                  Stop sharing
                </button>
              </div>
            </>
          )}
          {note && (
            <p className="label" role="status">
              {note}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * One answer and the question before it, as a picture to send or save. With `summarize` it opens on a 9:16 picture of a
 * short summary of the answer (#261), and the whole answer is one tap away; when no summary can be written, the whole
 * answer is shown and the reader is told.
 */
export function ShareImageDialog({
  question,
  answer,
  summarize,
  onClose,
}: {
  question: string;
  answer: string;
  summarize?: (signal: AbortSignal) => Promise<ApiResult<string>>;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [mode, setMode] = useState<"summary" | "full">(summarize ? "summary" : "full");
  const [summary, setSummary] = useState<string | null>(null);
  const [img, setImg] = useState<{ url: string; file: File } | "failed" | null>(null);
  const [note, setNote] = useState("");
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) {
      if (typeof d.showModal === "function") d.showModal();
      else d.setAttribute("open", "");
    }
  }, []);
  // The summary is asked for once, when it is first shown. The caller's function is new on each of its renders, so it is
  // read from a ref: a re-render while the model writes does not ask again.
  const ask = useRef(summarize);
  ask.current = summarize;
  useEffect(() => {
    if (mode !== "summary" || summary !== null || !ask.current) return;
    const ctl = new AbortController();
    void ask.current(ctl.signal).then((r) => {
      if (ctl.signal.aborted) return;
      if (r.ok) return setSummary(r.value);
      setNote("No summary could be written just now, so this is the whole answer.");
      setMode("full");
    });
    return () => ctl.abort();
  }, [mode, summary]);
  useEffect(() => {
    setImg(null);
    if (mode === "summary" && summary === null) return;
    let url = "";
    let live = true;
    void renderShareImage(question, answer, new Date(), mode === "summary" ? (summary ?? undefined) : undefined).then((blob) => {
      if (!live) return;
      if (!blob) return setImg("failed");
      url = URL.createObjectURL(blob);
      setImg({ url, file: new File([blob], "antares-answer.png", { type: "image/png" }) });
    });
    return () => {
      live = false;
      if (url) URL.revokeObjectURL(url);
    };
  }, [question, answer, mode, summary]);
  const ready = img !== null && img !== "failed" ? img : null;
  const canSend = ready !== null && typeof navigator.canShare === "function" && navigator.canShare({ files: [ready.file] });
  const send = () => {
    if (!ready) return;
    navigator.share({ files: [ready.file] }).catch((e: unknown) => {
      if (!(e instanceof DOMException && e.name === "AbortError")) setNote("Could not send it from here. Save the image instead.");
    });
  };
  return createPortal(
    <dialog ref={ref} className="settings-dialog share-image" aria-labelledby="share-image-title" onClose={onClose} onCancel={onClose}>
      <header>
        <h2 id="share-image-title">Share as image</h2>
        <button type="button" className="quiet icon-only" data-icon="close" aria-label="Close" onClick={onClose} />
      </header>
      <div className="share-image-body">
        {summarize && (
          <div className="share-image-mode" role="group" aria-label="What the picture shows">
            {(["summary", "full"] as const).map((m) => (
              <button
                key={m}
                type="button"
                className={mode === m ? "quiet on" : "quiet"}
                aria-pressed={mode === m}
                onClick={() => {
                  setNote("");
                  setMode(m);
                }}
              >
                {m === "summary" ? "Summary" : "Full answer"}
              </button>
            ))}
          </div>
        )}
        {img === null && (
          <p className="label" role="status">
            {mode === "summary" && summary === null ? "Summarising…" : "Drawing…"}
          </p>
        )}
        {img === "failed" && <p role="alert">This browser could not draw the image.</p>}
        {ready && <img src={ready.url} alt="The question and answer as an image" />}
        <div className="actions">
          {canSend && (
            <button type="button" data-icon="share" onClick={send}>
              Send…
            </button>
          )}
          <button type="button" className={canSend ? "quiet" : undefined} data-icon="download" disabled={!ready} onClick={() => ready && saveBlob(ready.file.name, ready.file)}>
            Save image
          </button>
        </div>
        {note && <p role="alert">{note}</p>}
      </div>
    </dialog>,
    document.body,
  );
}

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Turn } from "./chatApi";
import { fileName, loadAllTurns, saveText, toMarkdown } from "./exportChat";
import { Markdown } from "./Markdown";

/**
 * Export the open chat. Markdown downloads a file; PDF uses the browser's own
 * print-to-PDF on a print-only copy of the whole chat (the on-screen log is
 * virtualised, so printing it directly would drop turns). No server renderer.
 */
export function ExportChat({ chatId, title }: { chatId: string; title: string }) {
  const [state, setState] = useState<{ busy: string } | { error: string } | null>(null);
  const [printing, setPrinting] = useState<Turn[] | null>(null);

  // The chat that is open right now. ExportChat outlives a chat switch, so a run must compare
  // against this, not against the id its own closure captured.
  const open = useRef<string | null>(chatId);
  useEffect(() => {
    open.current = chatId;
    setState(null);
    setPrinting(null);
    return () => {
      open.current = null;
    };
  }, [chatId]);
  useEffect(() => {
    if (printing === null) return;
    const done = () => setPrinting(null);
    window.addEventListener("afterprint", done, { once: true });
    window.print(); // blocks until the dialog closes in most browsers
    return () => window.removeEventListener("afterprint", done);
  }, [printing]);

  const run = async (kind: "md" | "pdf") => {
    const id = chatId;
    setState({ busy: "Exporting…" });
    const r = await loadAllTurns(id, (n, total) => open.current === id && setState({ busy: `Exporting ${n} of ${total}…` }), () => open.current !== id);
    if (open.current !== id) return; // the user moved on; nothing is saved or printed from a chat they left
    if (!r.ok) return setState({ error: r.error });
    setState(null);
    if (kind === "md") saveText(fileName(title, "md"), toMarkdown(title, r.turns, new Date()));
    else setPrinting(r.turns);
  };

  const busy = state !== null && "busy" in state;
  return (
    <div className="export">
      <button type="button" className="quiet" data-icon="download" disabled={busy} onClick={() => void run("md")}>
        Export Markdown
      </button>
      <button type="button" className="quiet" data-icon="download" disabled={busy} onClick={() => void run("pdf")}>
        Export PDF
      </button>
      {state && (
        <span className="label" role={"error" in state ? "alert" : "status"}>
          {"busy" in state ? state.busy : `Export failed: ${state.error}`}
        </span>
      )}
      {printing &&
        createPortal(
          <div className="print-root">
            <h1>{title || "Antares chat"}</h1>
            <p className="label">Images, audio and video are not embedded; links stay as text.</p>
            {printing.map((t) => (
              <section key={t.seq} className={`print-turn ${t.role}`}>
                <h2>{t.role === "user" ? "You" : "Assistant"}</h2>
                {t.role === "user" ? <p className="plain">{t.text}</p> : <Markdown text={t.text} />}
              </section>
            ))}
          </div>,
          document.body,
        )}
    </div>
  );
}

import { useState } from "react";
import { changeChat, describeCode, type ChatSummary } from "./chatApi";

// The per-chat Visualize toggle (#153, LLM-008). On, this chat's next messages tell the assistant it may add a chart,
// table or card; what it sends is still checked and rendered by the safe renderer (web/src/openui). Saved on the
// server per chat; the checkbox shows what the server stored, not what was clicked.
// onSaving receives each pending save, so a message sent meanwhile waits for it: the run then reads the new setting.
export function VisualizeToggle({ chat, onChanged, onSaving }: { chat: ChatSummary; onChanged: (c: ChatSummary) => void; onSaving?: (p: Promise<{ ok: boolean }>) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <label className="visualize-toggle">
      <input
        type="checkbox"
        checked={chat.visualize}
        disabled={busy}
        onChange={async (e) => {
          setBusy(true);
          setError("");
          const pending = changeChat(chat.id, { visualize: e.target.checked });
          onSaving?.(pending);
          const r = await pending;
          setBusy(false);
          if (r.ok) onChanged(r.value);
          else setError(describeCode(r.code));
        }}
      />{" "}
      Visualize: charts, tables and cards in replies (this chat, from the next message)
      {error && <span role="alert"> {error}</span>}
    </label>
  );
}

/** The toggle for the chat on screen: from the loaded list, or (opened from a search result on a later page) the chat
 * that search read by id. Nothing is shown when neither is known. */
export function OpenChatVisualize({ chat, onChanged, onSaving }: { chat: ChatSummary | undefined; onChanged: (c: ChatSummary) => void; onSaving?: (p: Promise<{ ok: boolean }>) => void }) {
  return chat ? <VisualizeToggle chat={chat} onChanged={onChanged} onSaving={onSaving} /> : null;
}

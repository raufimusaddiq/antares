import { useState } from "react";
import { changeChat, describeCode, type ChatSummary } from "./chatApi";

// The per-chat Visualize toggle (#153, LLM-008). On, this chat's next messages tell the assistant it may add a chart,
// table or card; what it sends is still checked and rendered by the safe renderer (web/src/openui). Saved on the
// server per chat; the checkbox shows what the server stored, not what was clicked.
export function VisualizeToggle({ chat, onChanged }: { chat: ChatSummary; onChanged: (c: ChatSummary) => void }) {
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
          const r = await changeChat(chat.id, { visualize: e.target.checked });
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

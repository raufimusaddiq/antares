import { useEffect, useState } from "react";
import { changeChat, describeCode, getChat, type ChatSummary } from "./chatApi";

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

/** The toggle for the chat on screen. A chat opened from a search result may not be in the loaded list: then it is read
 * by id, so its setting shows (and can change) all the same. */
export function OpenChatVisualize({ chatId, listed, onChanged }: { chatId: string; listed: ChatSummary | undefined; onChanged: (c: ChatSummary) => void }) {
  const [fetched, setFetched] = useState<ChatSummary | null>(null);
  useEffect(() => {
    setFetched(null);
    if (listed) return;
    let live = true;
    void getChat(chatId).then((r) => live && r.ok && setFetched(r.value));
    return () => {
      live = false;
    };
  }, [chatId, listed === undefined]);
  const chat = listed ?? (fetched?.id === chatId ? fetched : null);
  if (!chat) return null;
  return (
    <VisualizeToggle
      chat={chat}
      onChanged={(c) => {
        if (!listed) setFetched(c);
        onChanged(c);
      }}
    />
  );
}

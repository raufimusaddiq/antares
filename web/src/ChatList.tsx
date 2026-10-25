import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { describeCode, type ApiResult, type ChatList as ChatPage, type ChatSummary } from "./chatApi";

// The conversation list and what the owner can do to a conversation: rename, pin, archive, delete.
// This component only asks; every change is made by the server (Chat.tsx passes the calls), and a change is
// shown only after the server confirms it. A failure stays visible where the action was taken.

/** Each action answers "" when the server confirmed it, otherwise words for the person. */
export interface ChatActions {
  rename: (chat: ChatSummary, title: string) => Promise<string>;
  pin: (chat: ChatSummary, pinned: boolean) => Promise<string>;
  archive: (chat: ChatSummary, archived: boolean) => Promise<string>;
  remove: (chat: ChatSummary) => Promise<string>;
}

type Dialog = { kind: "rename" | "delete"; chat: ChatSummary };

const label = (c: ChatSummary) => c.title || "New chat";

/** Focus goes back to where the person was; if that row is gone (a delete), to the first control of the list. */
function restoreFocus(target: HTMLElement | null) {
  if (target && document.contains(target)) target.focus();
  else document.querySelector<HTMLElement>(".side > button")?.focus();
}

export function ChatList(props: {
  chats: ChatSummary[];
  current: string | null;
  onOpen: (c: ChatSummary) => void;
  actions: ChatActions;
  /** Why deleting is not offered; "" when it is. Comes from the assistant's own capability answer. */
  deleteBlocked: string;
  listArchive: (cursor?: string) => Promise<ApiResult<ChatPage>>;
  /** Bumped when the chats changed elsewhere (a delete, a rename), so a loaded archive is read again. */
  archiveTick?: number;
  /** Shown right under the list: "Show more" for the next page of chats. */
  more?: ReactNode;
}) {
  const { chats, current, onOpen, actions, deleteBlocked, listArchive, archiveTick = 0, more } = props;
  const [menu, setMenu] = useState<{ chat: ChatSummary; x: number; y: number } | null>(null);
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [status, setStatus] = useState("");
  const [showArchive, setShowArchive] = useState(false);
  const [archive, setArchive] = useState<{ state: "loading" | "ready" | "error"; chats: ChatSummary[]; next: string; message: string }>({ state: "loading", chats: [], next: "", message: "" });
  const trigger = useRef<HTMLElement | null>(null);
  const toggleRef = useRef<HTMLButtonElement>(null);
  const toggled = useRef(false);
  // The toggle moves between the end of the list and the archive's header, so keyboard focus follows it.
  useEffect(() => {
    if (toggled.current) toggleRef.current?.focus();
  }, [showArchive]);

  const loadArchive = useCallback(
    async (cursor?: string) => {
      const r = await listArchive(cursor);
      setArchive((a) =>
        r.ok
          ? { state: "ready", chats: cursor ? [...a.chats, ...r.value.chats.filter((c) => !a.chats.some((x) => x.id === c.id))] : r.value.chats, next: r.value.nextCursor, message: "" }
          : { ...a, state: cursor ? a.state : "error", message: describeCode(r.code) },
      );
    },
    [listArchive],
  );
  useEffect(() => {
    if (showArchive) void loadArchive();
  }, [showArchive, archiveTick, loadArchive]);

  const openMenu = (chat: ChatSummary, el: HTMLElement) => {
    trigger.current = el;
    const r = el.getBoundingClientRect();
    const width = 14 * 16;
    setMenu({ chat, x: Math.max(8, Math.min(r.right - width, window.innerWidth - width - 8)), y: r.bottom + 4 });
  };
  const closeMenu = (refocus: boolean) => {
    setMenu(null);
    if (refocus) restoreFocus(trigger.current);
  };

  const run = async (work: Promise<string>, done: string) => {
    const problem = await work;
    setStatus(problem || done);
    if (!problem && showArchive) void loadArchive();
    return problem;
  };

  const row = (c: ChatSummary): ReactNode => (
    <li key={c.id}>
      <button type="button" className={c.id === current ? "item current" : "item"} aria-current={c.id === current ? "true" : undefined} onClick={() => onOpen(c)}>
        {label(c)}
        {c.pinned && <span className="tag"> Pinned</span>}
        {c.deleteFailed && <span className="tag warn"> Delete failed</span>}
      </button>
      <button
        type="button"
        className="quiet row-more"
        aria-label={`Actions for ${label(c)}`}
        aria-haspopup="menu"
        aria-expanded={menu?.chat.id === c.id}
        onClick={(e) => (menu?.chat.id === c.id ? closeMenu(true) : openMenu(c, e.currentTarget))}
      >
        <span aria-hidden="true">⋯</span>
      </button>
    </li>
  );

  const toggle = (
    <button
      ref={toggleRef}
      type="button"
      className="quiet"
      aria-pressed={showArchive}
      onClick={() => {
        toggled.current = true;
        setShowArchive((s) => !s);
      }}
    >
      {showArchive ? "Hide archived" : "Show archived"}
    </button>
  );

  return (
    <>
      <ul>
        {chats.map(row)}
        {!showArchive && <li className="archive-toggle">{toggle}</li>}
      </ul>
      {more}
      {showArchive && (
        <section className="archive" aria-label="Archived conversations">
          {toggle}
          {archive.state === "loading" && <p className="label">Loading…</p>}
          {archive.state === "error" && (
            <p className="label" role="alert">
              Archive unavailable: {archive.message}
            </p>
          )}
          {archive.state === "ready" && archive.chats.length === 0 && <p className="label">Nothing archived.</p>}
          <ul>{archive.chats.map(row)}</ul>
          {archive.state === "ready" && archive.message && (
            <p className="label" role="alert">
              {archive.message}
            </p>
          )}
          {archive.next && (
            <button type="button" className="quiet" onClick={() => void loadArchive(archive.next)}>
              Show more archived
            </button>
          )}
        </section>
      )}
      <p className="sr" role="status" aria-live="polite">
        {status}
      </p>
      {menu && (
        <ChatMenu
          chat={menu.chat}
          at={menu}
          deleteBlocked={deleteBlocked}
          onClose={closeMenu}
          onRename={() => {
            setMenu(null);
            setDialog({ kind: "rename", chat: menu.chat });
          }}
          onDelete={() => {
            setMenu(null);
            setDialog({ kind: "delete", chat: menu.chat });
          }}
          onPin={() => {
            const c = menu.chat;
            closeMenu(true);
            void run(actions.pin(c, !c.pinned), `${c.pinned ? "Unpinned" : "Pinned"} ${label(c)}.`);
          }}
          onArchive={() => {
            const c = menu.chat;
            // The row leaves this list, so focus cannot go back to it; restoreFocus falls back to the list's first control.
            closeMenu(false);
            void run(actions.archive(c, !c.archived), `${c.archived ? "Restored" : "Archived"} ${label(c)}.`).then(() => restoreFocus(trigger.current));
          }}
        />
      )}
      {dialog?.kind === "rename" && (
        <RenameDialog
          chat={dialog.chat}
          onCancel={() => {
            setDialog(null);
            restoreFocus(trigger.current);
          }}
          onSave={async (title) => {
            const problem = await run(actions.rename(dialog.chat, title), `Renamed to ${title}.`);
            if (!problem) {
              setDialog(null);
              restoreFocus(trigger.current);
            }
            return problem;
          }}
        />
      )}
      {dialog?.kind === "delete" && (
        <DeleteDialog
          chat={dialog.chat}
          onCancel={() => {
            setDialog(null);
            restoreFocus(trigger.current);
          }}
          onDelete={async () => {
            const problem = await run(actions.remove(dialog.chat), `Deleted ${label(dialog.chat)}.`);
            if (!problem) {
              setDialog(null);
              restoreFocus(trigger.current);
            }
            return problem;
          }}
        />
      )}
    </>
  );
}

// ---- the actions menu -----------------------------------------------------------------

function ChatMenu(props: {
  chat: ChatSummary;
  at: { x: number; y: number };
  deleteBlocked: string;
  onClose: (refocus: boolean) => void;
  onRename: () => void;
  onPin: () => void;
  onArchive: () => void;
  onDelete: () => void;
}) {
  const { chat, at, deleteBlocked, onClose } = props;
  const ref = useRef<HTMLDivElement>(null);
  const reasonId = useId();
  const deleteReason = deleteBlocked || (chat.activeRunId ? "Wait for the reply in progress to finish." : "");

  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    ref.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
    const away = (e: PointerEvent) => {
      const t = e.target as Element | null;
      if (!ref.current?.contains(t) && !t?.closest?.(".row-more")) close.current(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, []);

  const items = () => [...(ref.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
  const onKey = (e: KeyboardEvent) => {
    const list = items();
    const at = list.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => {
      e.preventDefault();
      list[(i + list.length) % list.length]?.focus();
    };
    switch (e.key) {
      case "ArrowDown":
        return go(at + 1);
      case "ArrowUp":
        return go(at - 1);
      case "Home":
        return go(0);
      case "End":
        return go(list.length - 1);
      case "Escape":
        e.preventDefault();
        return onClose(true);
      case "Tab":
        e.preventDefault();
        return onClose(true); // leaving the menu returns to its trigger, which is where the page's tab order resumes
    }
  };

  return (
    <div ref={ref} className="row-menu" style={{ left: at.x, top: at.y }} onKeyDown={onKey}>
      <div role="menu" aria-label={`Actions for ${label(chat)}`}>
      <button type="button" role="menuitem" onClick={props.onRename}>
        Rename…
      </button>
      {!chat.archived && (
        <button type="button" role="menuitem" onClick={props.onPin}>
          {chat.pinned ? "Unpin" : "Pin to top"}
        </button>
      )}
      <button type="button" role="menuitem" onClick={props.onArchive}>
        {chat.archived ? "Restore from archive" : "Archive"}
      </button>
      <button type="button" role="menuitem" className="danger" aria-disabled={deleteReason ? "true" : undefined} aria-describedby={deleteReason ? reasonId : undefined} onClick={deleteReason ? undefined : props.onDelete}>
        {chat.deleteFailed ? "Retry delete…" : "Delete…"}
      </button>
      </div>
      {deleteReason && (
        <p id={reasonId} className="label">
          {deleteReason}
        </p>
      )}
    </div>
  );
}

// ---- dialogs --------------------------------------------------------------------------

/** A modal that keeps Tab inside it and answers Escape. Focus starts on `initial` (or the first control). */
function useModal(onCancel: () => void, busy: boolean, initial?: string) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    const first = initial ? el?.querySelector<HTMLElement>(initial) : null;
    (first ?? el?.querySelector<HTMLElement>("input, button"))?.focus();
  }, []); // once per opening; `initial` is the caller's constant selector
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape" && !busy) {
      e.preventDefault();
      e.stopPropagation();
      onCancel();
    } else if (e.key === "Tab") {
      const f = [...(ref.current?.querySelectorAll<HTMLElement>("input, button:not(:disabled)") ?? [])];
      if (f.length === 0) return;
      const i = f.indexOf(document.activeElement as HTMLElement);
      if (e.shiftKey && i <= 0) {
        e.preventDefault();
        f[f.length - 1].focus();
      } else if (!e.shiftKey && i === f.length - 1) {
        e.preventDefault();
        f[0].focus();
      }
    }
  };
  return { ref, onKeyDown };
}

function RenameDialog({ chat, onCancel, onSave }: { chat: ChatSummary; onCancel: () => void; onSave: (title: string) => Promise<string> }) {
  const [title, setTitle] = useState(chat.title);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const heading = useId();
  const field = useId();
  const modal = useModal(onCancel, busy, "input");
  const clean = title.trim();
  const submit = async () => {
    if (clean === "" || busy) return;
    setBusy(true);
    setError("");
    const problem = await onSave(clean);
    setBusy(false);
    if (problem) setError(problem);
  };
  return (
    <div className="scrim">
      <div ref={modal.ref} className="dialog" role="dialog" aria-modal="true" aria-labelledby={heading} onKeyDown={modal.onKeyDown}>
        <h2 id={heading}>Rename conversation</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <label htmlFor={field}>Name</label>
          <input id={field} value={title} maxLength={120} autoComplete="off" aria-invalid={error ? "true" : undefined} onChange={(e) => setTitle(e.target.value)} />
          {error && (
            <p className="notice" role="alert">
              {error}
            </p>
          )}
          <div className="dialog-actions">
            <button type="button" className="quiet" disabled={busy} onClick={onCancel}>
              Cancel
            </button>
            <button type="submit" disabled={busy || clean === ""}>
              {busy ? "Saving…" : "Save"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function DeleteDialog({ chat, onCancel, onDelete }: { chat: ChatSummary; onCancel: () => void; onDelete: () => Promise<string> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const heading = useId();
  const body = useId();
  // Focus starts on Cancel: the destructive choice is never the default.
  const modal = useModal(onCancel, busy, "[data-cancel]");
  const confirm = async () => {
    setBusy(true);
    setError("");
    const problem = await onDelete();
    setBusy(false);
    if (problem) setError(problem);
  };
  return (
    <div className="scrim">
      <div ref={modal.ref} className="dialog" role="alertdialog" aria-modal="true" aria-labelledby={heading} aria-describedby={body} onKeyDown={modal.onKeyDown}>
        <h2 id={heading}>Delete “{label(chat)}”?</h2>
        <p id={body}>
          This deletes the conversation from the assistant as well as from Antares, with its unsent draft, its search entries and the images sent in it. It cannot be undone.
          {chat.deleteFailed && " The last attempt did not finish; nothing was lost."}
        </p>
        {error && (
          <p className="notice" role="alert">
            {error} The conversation is still here.
          </p>
        )}
        <div className="dialog-actions">
          <button type="button" className="quiet" data-cancel disabled={busy} onClick={onCancel}>
            Cancel
          </button>
          <button type="button" className="danger" disabled={busy} onClick={() => void confirm()}>
            {busy ? "Deleting…" : error ? "Try again" : "Delete"}
          </button>
        </div>
      </div>
    </div>
  );
}

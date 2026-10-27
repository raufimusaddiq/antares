import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import {
  cancelRun,
  changeChat,
  createChat,
  deleteChat,
  describeCode,
  describeTurns,
  describeUsage,
  describeSpending,
  reconcileSpending,
  parseRupiah,
  spendingFields,
  putSpending,
  type Spending,
  describeGlance,
  parseLimitInput,
  getChat,
  getMessages,
  getRun,
  getCapabilities,
  getUsage,
  putBudget,
  listChats,
  newIdempotencyKey,
  runEventsUrl,
  sendMessage,
  steerRun,
  enqueueMessage,
  type ChatQueue,
  type ChatSummary,
  type ChatUsage,
  type Inventory,
  offers,
  type SearchResult,
  type Turn,
} from "./chatApi";
import { ModelPicker } from "./ModelPicker";
import { mergeNewest, prependOlder, reachOf } from "./history";
import { SearchPanel } from "./Search";
import { ChatList, type ChatActions } from "./ChatList";
import { MessageBody } from "./MessageBody";
import { Markdown } from "./Markdown";
import { MAX_REPLY, ReplyContext } from "./openui/reply";
import { Artifacts } from "./Artifacts";
import { ExportChat } from "./ExportChat";
import { OpenChatVisualize } from "./VisualizeToggle";
import { MemorySkillsPanel } from "./MemorySkills";
import { UsageDashboard } from "./UsageDashboard";
import { JobsPanel } from "./Jobs";
import { fenceSelection, inFence, indentInFence, toggleList, wrapSelection, type Edit } from "./composerEdit";
import { Notifications } from "./Notifications";
import { ImageStrip, useImageAttachments, type Attachment } from "./ImageAttachments";
import { SentImage } from "./MediaPreview";
import { describeImageReason, describeImageSend, getImageInput, imageFilesFrom, sendMessageWithImages, IMAGE_TYPES, type ImageInput } from "./mediaApi";
import { hideMediaPaths } from "./mediaTags";
import { planRetry } from "./retry";
import { VirtualList } from "./VirtualList";
import { EVENT_NAMES, describeFolded, describeTool, isTerminal, newRun, parseEvent, reduce, runNotice, type LiveRun, type RunEvent } from "./runState";
import { AntaresMark } from "./brand/AntaresMark";
import { Recorder } from "./Recorder";
import { Dictation } from "./Dictation";
import type { AttachmentRef } from "./MediaPreview";
import { RunPrompts } from "./RunPrompts";
import { QueuePanel } from "./QueuePanel";
import { forgetChatDrafts, loadDraft, matchTemplates, mergeTemplates, saveDraft, type Template } from "./drafts";
import { DraftNotice, sharedTextApplied, takeSharedText, useDraftSync, NO_HOLD, type SendHold } from "./DraftSync";
import { listOwnTemplates, type OwnTemplate } from "./draftApi";
import { Templates } from "./Templates";
import { AppearancePanel, DiagnosticsPanel } from "./Settings";

const MAX_INPUT = 32_000;
const RECONNECT_TRIES = 5;

type Action = { k: "start"; runId: string } | { k: "event"; ev: RunEvent } | { k: "maybe" } | { k: "clear" };

function runReducer(state: LiveRun | null, a: Action): LiveRun | null {
  switch (a.k) {
    case "start":
      return state && state.runId === a.runId ? state : newRun(a.runId);
    case "event":
      return state ? reduce(state, a.ev) : state;
    case "maybe":
      return state ? { ...state, maybeStillRunning: true } : state;
    case "clear":
      return null;
  }
}

interface Pending {
  input: string;
  key: string;
  failure: string; // empty while sending
  hold: SendHold; // this send's claim on the server draft, kept for a resend
  images?: AttachmentRef[]; // the staged images sent with it; a resend or retry carries them again, never drops them
}

// ---- presentational pieces (no hooks: rendered to static markup in tests) -----

export function RunView({ run, onCancel, onRetry, onReconnect }: { run: LiveRun; onCancel: () => void; onRetry: () => void; onReconnect: () => void }) {
  const notice = runNotice(run);
  const live = run.phase === "streaming" || run.phase === "reconnecting";
  const disconnected = run.phase === "disconnected" || run.phase === "detached";
  return (
    <article className={`turn assistant run-${run.phase}`} aria-label="Assistant reply">
      <h2 className="who">Assistant</h2>
      {live && run.text === "" && <p className="label">{run.thinking ? "Thinking…" : "Working…"}</p>}
      <Activity run={run} live={live} />
      {run.text !== "" && (
        <div className={run.phase === "completed" ? "" : "partial"}>
          {run.phase !== "completed" && !live && <p className="label">Partial response</p>}
          <MessageBody text={hideMediaPaths(run.text).text} streaming={live} />
        </div>
      )}
      {notice && (
        <p className="notice" role={run.phase === "reconnecting" ? "status" : "alert"}>
          {notice}
        </p>
      )}
      {(live || disconnected) && (
        <button type="button" className="quiet" onClick={onCancel}>
          Cancel
        </button>
      )}
      {disconnected && (
        <button type="button" className="quiet" onClick={onReconnect}>
          Reconnect
        </button>
      )}
      {(run.phase === "failed" || run.phase === "cancelled") && (
        <button type="button" className="quiet" onClick={onRetry}>
          Try again
        </button>
      )}
    </article>
  );
}

// Activity is one group per reply: open while the reply runs, folded once it ends, and opened again on request.
// A row shows the tool's name, its state and duration, and the result preview only when the server sent one.
function Activity({ run, live }: { run: LiveRun; live: boolean }) {
  const n = run.tools.length + run.folded.count;
  if (n === 0) return null;
  const failed = run.folded.failed + run.tools.filter((t) => t.state === "failed").length;
  return (
    <details className="activity" key={live ? "live" : "ended"} open={live || undefined}>
      <summary>
        Activity: {n} tool {n === 1 ? "call" : "calls"}
        {failed > 0 ? `, ${failed} failed` : ""}
      </summary>
      <ul className="tools" aria-label="Tool activity">
        {run.folded.count > 0 && (
          <li className="label">{describeFolded(run.folded)}</li>
        )}
        {run.tools.map((t, i) => (
          <li key={`${t.id}-${i}`} data-tool={t.state}>
            {t.label || "Tool"}: {describeTool(t, run.phase)}
            {t.summary && <span className="plain"> {t.summary}</span>}
          </li>
        ))}
      </ul>
    </details>
  );
}

const turnKey = (t: Turn) => String(t.seq);

/**
 * Regenerate, edit and rewind (PRD §21.5, UXP-ACC-012). The assistant backend has no route for any of them, so each
 * is a button that cannot be activated and says why, in text, from the server's capability answer.
 * There is deliberately no action handler: when a verified route lands, this is where it is wired. A press only
 * shows or hides the reason next to the button, so it is reachable by tap (iOS Safari does not focus a tapped
 * button, so :hover and :focus alone would leave a phone user without it). The reason is always in the document
 * and always the button's description, whether or not it is currently shown.
 */
const TURN_CONTROLS = {
  regenerate: { id: "regenerate", label: "Regenerate" },
  edit: { id: "edit_turn", label: "Edit" },
  rewind: { id: "rewind", label: "Rewind" },
} as const;

export function turnControlReason(inv: Inventory | null, id: string): string {
  const row = inv?.capabilities.find((c) => c.id === id);
  if (!row) return "Availability unknown";
  return row.available ? "Not available in this app yet" : row.reason || "Not supported by the assistant backend";
}

/**
 * The working alternative beside the turn controls the assistant cannot honour (#166): it writes the earlier text into
 * the composer as a new message. Nothing is rewound, edited or sent; the user sends it, or not.
 */
export interface TurnReuse {
  label: string;
  onClick: () => void;
}

export function TurnControls({ inv, kinds, seq, reuse }: { inv: Inventory | null; kinds: readonly (keyof typeof TURN_CONTROLS)[]; seq: number; reuse?: TurnReuse }) {
  const [shown, setShown] = useState<string | null>(null);
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (shown === null) return;
    // A press anywhere else, or Escape, puts the reason away again.
    const away = (e: Event) => {
      if (e.type === "keydown" ? (e as globalThis.KeyboardEvent).key === "Escape" : !ref.current?.contains(e.target as Node)) setShown(null);
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", away);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", away);
    };
  }, [shown]);
  return (
    <div className="turn-controls" role="group" aria-label="Turn controls" ref={ref}>
      {kinds.map((k) => {
        const c = TURN_CONTROLS[k];
        const rid = `tc-${k}-${seq}`;
        return (
          <span key={k} className="turn-control" data-reason={shown === k ? "shown" : undefined}>
            <button type="button" className="quiet" aria-disabled="true" aria-describedby={rid} data-control={k} data-icon={k} onClick={() => setShown(shown === k ? null : k)}>
              {c.label}
            </button>
            <small id={rid} className="label">
              {turnControlReason(inv, c.id)}
              {reuse && k !== "rewind" && !offers(inv, c.id) ? ` Instead, use “${reuse.label}”: it writes a new message for you to send.` : ""}
            </small>
          </span>
        );
      })}
      {reuse && (
        <button type="button" className="quiet" data-control="reuse" onClick={reuse.onClick}>
          {reuse.label}
        </button>
      )}
    </div>
  );
}

/** The images a user turn carried. Previews load on demand from the owner's asset route. */
function SentImages({ refs }: { refs: readonly AttachmentRef[] }) {
  if (refs.length === 0) return null;
  return (
    <div data-media="sent" role="group" aria-label="Images you sent">
      {refs.map((r, i) => (
        <SentImage key={r.assetId} att={r} index={i} />
      ))}
    </div>
  );
}

export interface TranscriptProps {
  turns: Turn[];
  pending: Omit<Pending, "hold"> | null;
  run: LiveRun | null;
  /** More (older) turns exist on the server. */
  hasMore?: boolean;
  loadingMore?: boolean;
  olderError?: string;
  onLoadMore?: () => void;
  /** The turn a search result opened at: scrolled to and marked. */
  focusSeq?: number | null;
  focusNonce?: number;
  /** The chat these turns belong to; without it nothing is offered for download. */
  chatId?: string;
  /** The capability answer the turn controls read; null shows them disabled as unknown. */
  inventory?: Inventory | null;
  /** Bumped when the run's stream reports a prompt opened or closed; the prompts themselves come from the server. */
  promptTick?: number;
  /** Puts an earlier message's text into the composer as a new message (the alternative to edit and regenerate). */
  onReuse?: (text: string) => void;
  onCancel: () => void;
  onRetry: () => void;
  onReconnect: () => void;
  onResend: () => void;
}

const never = () => {};

/** The chat with the latest activity: the list puts pinned chats first, but a person lands where they last were. */
const mostRecent = (l: ChatSummary[]): ChatSummary => l.reduce((a, b) => (b.updatedAt > a.updatedAt ? b : a));

/** Words for a failed management action. The server's own text is never shown. */
function describeManage(code: string, what: "change" | "delete"): string {
  if (what === "delete" && code === "RUN_CONFLICT") return "The assistant is still working on a reply in this conversation. Wait for it to finish, then delete.";
  if (what === "delete" && code === "CHAT_STATE") return "This conversation is already being deleted, here or in another window. It is still here until that finishes; check again in a moment.";
  if (what === "delete" && code === "CAPABILITY_UNAVAILABLE") return "The assistant does not offer deleting conversations, so nothing was deleted.";
  if (what === "delete" && (code === "HERMES_UNAVAILABLE" || code === "UPSTREAM_TIMEOUT")) return "The assistant did not confirm the delete, so the conversation was not deleted. Try again shortly.";
  return describeCode(code);
}

export function Transcript(props: TranscriptProps) {
  const { turns, pending, run, hasMore = false, loadingMore = false, olderError = "", onLoadMore = never, focusSeq = null, focusNonce = 0, chatId, inventory = null, onReuse } = props;
  // Compact: the controls sit on the newest turn of each role only, not on every message.
  const lastUser = turns.reduce((n, t) => (t.role === "user" ? t.seq : n), -1);
  const lastAssistant = turns.reduce((n, t) => (t.role === "assistant" ? t.seq : n), -1);
  // "Ask again" reuses the question the newest answer replied to: the newest user turn before it.
  const askedText = turns.reduce((q, t) => (t.role === "user" && t.seq < lastAssistant ? t.text : q), "");
  const renderTurn = useCallback(
    (t: Turn) => (
      <article className={`turn ${t.role}`} data-found={t.seq === focusSeq ? "true" : undefined} aria-current={t.seq === focusSeq ? "location" : undefined}>
        <h2 className="who">{t.role === "user" ? "You" : "Assistant"}</h2>
        {t.role === "user" ? <Markdown text={t.text} /> : <MessageBody text={t.role === "assistant" ? hideMediaPaths(t.text).text : t.text} streaming={false} />}
        {t.role === "user" && t.attachments && <SentImages refs={t.attachments} />}
        {t.role === "assistant" && chatId && t.ref && <Artifacts chatId={chatId} seq={t.seq} turnRef={t.ref} text={t.text} />}
        {chatId && t.role === "user" && t.seq === lastUser && (
          <TurnControls inv={inventory} kinds={["edit"]} seq={t.seq} reuse={onReuse && { label: "Edit as new message", onClick: () => onReuse(t.text) }} />
        )}
        {chatId && t.role === "assistant" && t.seq === lastAssistant && (
          <TurnControls inv={inventory} kinds={["regenerate", "rewind"]} seq={t.seq} reuse={onReuse && askedText ? { label: "Ask again", onClick: () => onReuse(askedText) } : undefined} />
        )}
      </article>
    ),
    [focusSeq, chatId, inventory, lastUser, lastAssistant, onReuse, askedText],
  );
  const note = loadingMore ? (
    "Loading older messages…"
  ) : olderError ? (
    <>
      Older messages unavailable: {olderError}{" "}
      <button type="button" className="quiet" onClick={onLoadMore}>
        Retry
      </button>
    </>
  ) : hasMore ? (
    "Scroll up for older messages"
  ) : turns.length > 0 ? (
    "Start of the conversation"
  ) : (
    ""
  );
  const footer = (
    <>
      {pending && (
        <article className="turn user pending">
          <h2 className="who">You</h2>
          <Markdown text={pending.input} />
          <SentImages refs={pending.images ?? []} />
          {pending.failure ? (
            <p className="notice" role="alert">
              Not sent: {pending.failure}{" "}
              <button type="button" className="quiet" onClick={props.onResend}>
                Send again
              </button>
            </p>
          ) : (
            <p className="label">Sending…</p>
          )}
        </article>
      )}
      {run && !isTerminal(run.phase) && <RunPrompts runId={run.runId} tick={props.promptTick ?? 0} />}
      {run && <RunView run={run} onCancel={props.onCancel} onRetry={props.onRetry} onReconnect={props.onReconnect} />}
    </>
  );
  return (
    <VirtualList
      items={turns}
      itemKey={turnKey}
      render={renderTurn}
      footer={footer}
      label="Conversation"
      hasMore={hasMore && !olderError}
      loadingMore={loadingMore}
      topNote={note}
      onLoadMore={onLoadMore}
      focusKey={focusSeq === null ? undefined : String(focusSeq)}
      focusNonce={focusNonce}
    />
  );
}

// On macOS Ctrl+B/E are caret movement in a text field, so the shortcuts there are Cmd only.
const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

/** What the composer needs to attach images. Absent = the composer is text only. */
export interface ComposerImages {
  /** The server's verdict for this chat's model (GET /api/chats/{id}/input). False hides the picker. */
  available: boolean;
  reason: string | null;
  items: Attachment[];
  notice: string | null;
  /** An image is still uploading or failed: sending waits, nothing is dropped. */
  pending: boolean;
  onFiles: (files: File[]) => void;
  onRemove: (key: string) => void;
  onRetry: (key: string) => void;
}

const IMAGE_ACCEPT = IMAGE_TYPES.join(",");

/** Where a steer stands. "late" is a refusal from a run that had already ended; "refused" is the assistant declining a live one. */
export type SteerNote = "sending" | "accepted" | "refused" | "late" | "failed";

/** Steer is its own intent beside Send (PRD §21.5, issue #149): present only while a live run can take it. */
export interface ComposerSteer {
  available: boolean;
  note: SteerNote | null;
  failure: string;
  onSteer: () => void;
}

const STEER_WORDS: Record<SteerNote, string> = {
  sending: "Sending guidance…",
  accepted: "Guidance accepted. The assistant will take it into account in this reply.",
  refused: "The assistant did not accept guidance right now. Your text is still here.",
  late: "Too late: the reply had already finished. Your text is still here; send it as a new message.",
  failed: "",
};

/** Queue is the third intent (PRD §27.4, issue #110): while a reply runs, the text waits to be sent as a new message after it. */
export interface ComposerQueue {
  failure: string;
  onQueue: () => void;
}

export function Composer(props: { value: string; busy: boolean; onChange: (v: string) => void; onSubmit: () => void; templates?: readonly Template[]; images?: ComposerImages; steer?: ComposerSteer; queue?: ComposerQueue }) {
  const steer = props.steer;
  const queue = props.queue;
  const img = props.images;
  const blocked = img?.pending === true;
  const empty = props.value.trim() === "";
  const [refusal, setRefusal] = useState("");
  const picker = useRef<HTMLInputElement>(null);
  const camera = useRef<HTMLInputElement>(null);
  // An image the model cannot take is refused out loud, never discarded and never sent anyway.
  const takeFiles = (files: File[]): boolean => {
    if (files.length === 0 || !img) return false;
    if (img.available) {
      setRefusal("");
      img.onFiles(files);
    } else {
      setRefusal(describeImageReason(img.reason));
    }
    return true;
  };
  const coarse = typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
  const matches = matchTemplates(props.value, props.templates);
  const insert = (t: Template) => {
    props.onChange(t.body);
    document.getElementById("composer-input")?.focus();
  };
  const area = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const onShortcut = (e: globalThis.KeyboardEvent) => {
      if (e.code !== "KeyM" || !e.altKey || !e.shiftKey || e.ctrlKey || e.metaKey || e.defaultPrevented || e.isComposing) return;
      if (e.target instanceof HTMLElement && e.target.closest("input, textarea, select, [contenteditable]")) return;
      e.preventDefault();
      area.current?.focus();
    };
    window.addEventListener("keydown", onShortcut);
    return () => window.removeEventListener("keydown", onShortcut);
  }, []);
  const caret = useRef<[number, number] | null>(null);
  useLayoutEffect(() => {
    if (caret.current && area.current) area.current.setSelectionRange(...caret.current);
    caret.current = null;
  }, [props.value]);
  const apply = (e: KeyboardEvent<HTMLTextAreaElement>, edit: Edit | null) => {
    if (!edit) return;
    e.preventDefault();
    caret.current = [edit.start, edit.end];
    props.onChange(edit.value);
  };
  // Escape inside a code fence hands Tab back to the browser until the next other key (no keyboard trap).
  const released = useRef(false);
  const [releasedNote, setReleasedNote] = useState(false);
  const composing = useRef(false);
  const release = (on: boolean) => {
    released.current = on;
    setReleasedNote(on);
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    const t = e.currentTarget;
    // Mid-composition keys (an IME's Enter, Tab or shortcut) belong to the IME. Safari reports the Enter
    // that ends a composition after compositionend, with keyCode 229 and isComposing already false.
    if (e.nativeEvent.isComposing || e.keyCode === 229 || composing.current) return;
    if (["Shift", "Control", "Alt", "Meta"].includes(e.key)) return;
    if (e.key === "Escape") {
      if (inFence(t.value, t.selectionStart)) release(true);
      return;
    }
    const wasReleased = released.current;
    if (released.current) release(false);
    const mod = (IS_MAC ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && !e.altKey;
    if (mod && !e.shiftKey) {
      apply(e, wrapSelection(t.value, t.selectionStart, t.selectionEnd, e.key));
    } else if (mod && e.code === "Digit8") {
      apply(e, toggleList(t.value, t.selectionStart, t.selectionEnd));
    } else if (mod && e.code === "KeyE") {
      apply(e, fenceSelection(t.value, t.selectionStart, t.selectionEnd));
    } else if (e.key === "Tab" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && !wasReleased) {
      apply(e, indentInFence(t.value, t.selectionStart, t.selectionEnd));
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      if (matches.length > 0) insert(matches[0]); // an open template menu: Enter picks, it does not send "/sum"
      else if (!empty && !props.busy && !blocked) props.onSubmit();
    }
  };
  return (
    <form
      className="composer"
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        if (!empty && !props.busy && !blocked) props.onSubmit();
      }}
      onDragOver={(e) => {
        if (img && Array.from(e.dataTransfer?.types ?? []).includes("Files")) e.preventDefault();
      }}
      onDrop={(e) => {
        const files = imageFilesFrom(e.dataTransfer);
        if (takeFiles(files)) e.preventDefault();
      }}
    >
      {img && <ImageStrip items={img.items} onRemove={img.onRemove} onRetry={img.onRetry} />}
      {(refusal || img?.notice) && (
        <p data-media="notice" role="alert">
          {refusal || img?.notice}
        </p>
      )}
      {matches.length > 0 && (
        <ul className="templates" aria-label="Templates">
          {matches.map((t) => (
            <li key={t.name}>
              <button type="button" className="quiet" onClick={() => insert(t)}>
                /{t.name}
              </button>
            </li>
          ))}
        </ul>
      )}
      <label htmlFor="composer-input" className="sr">
        Message
      </label>
      <textarea
        ref={area}
        id="composer-input"
        aria-keyshortcuts="Alt+Shift+M"
        rows={2}
        maxLength={MAX_INPUT}
        value={props.value}
        placeholder="Write a message"
        onChange={(e) => props.onChange(e.target.value)}
        aria-describedby="composer-hint"
        onKeyDown={onKey}
        onPaste={(e) => {
          // Only a pasted image is taken over; pasted text goes in as usual.
          const files = imageFilesFrom(e.clipboardData);
          if (takeFiles(files)) e.preventDefault();
        }}
        onBlur={() => release(false)}
        onCompositionStart={() => (composing.current = true)}
        onCompositionEnd={() => (composing.current = false)}
      />
      <span id="composer-hint" className="sr">
        Enter sends, Shift+Enter adds a line. Inside a code block, Tab indents; press Escape then Tab, or Shift+Tab, to move on.
      </span>
      <span className="sr" role="status">
        {releasedNote ? "Tab now moves focus out of the code block." : ""}
      </span>
      {img?.available && (
        <>
          <input
            ref={picker}
            type="file"
            accept={IMAGE_ACCEPT}
            multiple
            hidden
            aria-label="Choose images"
            onChange={(e) => {
              takeFiles(Array.from(e.target.files ?? []));
              e.target.value = "";
            }}
          />
          <button type="button" className="quiet" data-icon="clip" onClick={() => picker.current?.click()}>
            <span className="btn-label">Attach image</span>
          </button>
          {coarse && (
            <>
              <input
                ref={camera}
                type="file"
                accept={IMAGE_ACCEPT}
                capture="environment"
                hidden
                aria-label="Take a photo"
                onChange={(e) => {
                  takeFiles(Array.from(e.target.files ?? []));
                  e.target.value = "";
                }}
              />
              <button type="button" className="quiet" data-icon="camera" onClick={() => camera.current?.click()}>
                <span className="btn-label">Take photo</span>
              </button>
            </>
          )}
        </>
      )}
      {blocked && (
        <span data-media="wait" role="status">
          Waiting for images: let uploads finish, or retry or remove a failed one.
        </span>
      )}
      {steer?.available && (
        <button type="button" onClick={steer.onSteer} disabled={empty || blocked || steer.note === "sending"} aria-describedby="steer-note">
          <span className="btn-label">Steer</span>
        </button>
      )}
      {queue && (
        <button type="button" onClick={queue.onQueue} disabled={empty || blocked}>
          <span className="btn-label">Queue</span>
        </button>
      )}
      {queue?.failure && (
        <span role="alert" data-queue="failed">
          {queue.failure}
        </span>
      )}
      {steer?.note && (
        <span id="steer-note" role="status" data-steer={steer.note}>
          {steer.note === "failed" ? steer.failure : STEER_WORDS[steer.note]}
        </span>
      )}
      <button type="submit" disabled={empty || props.busy || blocked}>
        <span className="btn-label">Send</span>
        <svg className="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
          <path d="M5 12h14M13 6l6 6-6 6" />
        </svg>
      </button>
    </form>
  );
}

// ---- the app --------------------------------------------------------------------

export function Chat({ email, onSignOut, canAdmin = false, onAdmin, initialChatId = null, signOutError = "" }: { email: string; onSignOut: (everywhere?: boolean) => void; canAdmin?: boolean; onAdmin?: () => void; initialChatId?: string | null; signOutError?: string }) {
  const [chats, setChats] = useState<ChatSummary[]>([]);
  const [listState, setListState] = useState<"loading" | "ready" | "error">("loading");
  const [current, setCurrent] = useState<string | null>(null);
  const [turns, setTurns] = useState<Turn[]>([]);
  const [turnsError, setTurnsError] = useState("");
  const [cursor, setCursor] = useState(""); // where the next older page starts; "" = start of the conversation
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState("");
  const [chatsCursor, setChatsCursor] = useState("");
  const [loadingChats, setLoadingChats] = useState(false);
  const [actionsSlot, setActionsSlot] = useState<HTMLElement | null>(null); // the phone's home for the open chat's actions
  const [relist, setRelist] = useState(0); // bumped to read the chat list again (a chat came back from the archive)
  const [archiveTick, setArchiveTick] = useState(0); // the archive is read again after a change that touches it
  const [searchEpoch, setSearchEpoch] = useState(0); // a rename or delete makes an open search's titles and hits stale
  const chatsRef = useRef<ChatSummary[]>([]);
  chatsRef.current = chats;
  const openTitle = useRef(""); // title of the open chat, kept when it leaves the list (archived)
  const [run, dispatch] = useReducer(runReducer, null);
  const [promptTick, setPromptTick] = useState(0);
  const [pending, setPending] = useState<Pending | null>(null);
  const [draft, setDraft] = useState("");
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const draftNow = useRef(""); // what the box holds right now, for code that runs after an await
  draftNow.current = draft;
  // Typing writes the draft of the chat on screen. Switching chats loads that chat's draft;
  // nothing else ever writes, so text typed in one chat cannot land in another's key.
  const onDraft = (v: string) => {
    setDraft(v);
    saveDraft(email, currentRef.current, v);
  };
  // A rich block's button/input adds text to the draft of the chat on screen and never sends it.
  const addToDraft = (t: string) => {
    onDraft((draft.trim() === "" ? "" : draft.replace(/\s*$/, "\n")) + t.slice(0, MAX_REPLY));
    document.getElementById("composer-input")?.focus();
  };
  // Edit as new message / Ask again: the earlier text goes into the composer, after anything already typed there.
  // Stable for the transcript's memoised rows; it always writes into the draft as it is now.
  const reuseInto = useRef<(t: string) => void>(() => {});
  reuseInto.current = (t: string) => {
    onDraft((draftRef.current.trim() === "" ? "" : draftRef.current.replace(/\s*$/, "\n\n")) + t.slice(0, MAX_INPUT));
    document.getElementById("composer-input")?.focus();
  };
  const reuseText = useCallback((t: string) => reuseInto.current(t), []);
  const [banner, setBanner] = useState("");
  // Phone layout: keep the open conversation's chip in view in the sideways-scrolling strip.
  useEffect(() => {
    document.querySelector<HTMLElement>(".side button.item.current")?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [current, chats]);
  // The account actions live behind the Menu toggle at every width (rail footer on a wide screen, header on a phone); the
  // per-chat options are behind their toggle on a phone only. Both stay one tap away.
  const [menuOpen, setMenuOpen] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);
  useEffect(() => {
    if (!menuOpen) return;
    const away = (e: PointerEvent) => {
      if (!(e.target as Element | null)?.closest?.(".bar")) setMenuOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [menuOpen]);
  const [focus, setFocus] = useState<{ chat: string; seq: number; nonce: number } | null>(null); // a search result being opened at its turn

  useEffect(() => setDraft(loadDraft(email, current)), [email, current]);
  // Text the OS shared to the app: it waits in the new-chat draft, never in a chat and never sent.
  const [share, setShare] = useState<string | null>(takeSharedText);
  const sync = useDraftSync(email, current, draft, onDraft, share, () => {
    sharedTextApplied();
    setShare(null);
  });

  const [ownTemplates, setOwnTemplates] = useState<OwnTemplate[]>([]);
  useEffect(() => {
    void listOwnTemplates().then((l) => l && setOwnTemplates(l));
  }, []);

  const es = useRef<EventSource | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const attempts = useRef(0);
  const currentRef = useRef<string | null>(null);
  const att = useImageAttachments();
  const { discard: discardImages, release: releaseImages } = att;
  const turnsRef = useRef<Turn[]>([]);
  const cursorRef = useRef("");
  const blankChat = useRef(""); // id of the chat New chat created and nothing has been sent into yet
  const creating = useRef(false); // a New chat request is in flight: a second click must not create a second chat
  const focusCount = useRef(0); // distinguishes opening the same result twice
  const nav = useRef(0); // bumped whenever the reader picks a chat, so a slow create cannot override that choice
  const olderInflight = useRef(""); // "chat|cursor" of the older-page request in flight: one at a time, never twice
  currentRef.current = current;
  turnsRef.current = turns;
  cursorRef.current = cursor;

  const detach = useCallback(() => {
    es.current?.close();
    es.current = null;
    window.clearTimeout(timer.current);
  }, []);

  useEffect(() => {
    document.body.classList.add("chat");
    return () => {
      document.body.classList.remove("chat");
      detach();
    };
  }, [detach]);

  // The newest page. "replace" opens a chat; "merge" refreshes the tail (after a
  // run) without throwing away older pages the reader already scrolled back to.
  const loadTurns = useCallback(async (chatId: string, mode: "replace" | "merge" = "merge"): Promise<boolean> => {
    const r = await getMessages(chatId);
    if (currentRef.current !== chatId) return false;
    if (!r.ok) {
      setTurnsError(describeCode(r.code));
      return false;
    }
    setTurnsError("");
    const page = r.value;
    if (mode === "replace") {
      setTurns(page.turns);
      setCursor(page.nextCursor);
      return true;
    }
    const m = mergeNewest(turnsRef.current, page.turns);
    setTurns(m.turns);
    // The held list begins after this page's start only when nothing older was held.
    if (m.reset || m.turns.length === 0 || m.turns[0].seq >= (page.turns[0]?.seq ?? 0)) setCursor(page.nextCursor);
    return true;
  }, []);

  const loadOlder = useCallback(async () => {
    const chatId = currentRef.current;
    const at = cursorRef.current;
    if (!chatId || !at) return;
    const key = `${chatId}|${at}`;
    if (olderInflight.current === key || olderInflight.current !== "") return;
    olderInflight.current = key;
    setLoadingOlder(true);
    setOlderError("");
    const r = await getMessages(chatId, at);
    if (olderInflight.current === key) olderInflight.current = "";
    if (currentRef.current !== chatId) return;
    setLoadingOlder(false);
    if (!r.ok) {
      if (r.code === "HISTORY_CHANGED") {
        // The transcript no longer lines up with what is on screen: start again from the newest page.
        setBanner(describeCode(r.code) + " It has been reloaded.");
        void loadTurns(chatId, "replace");
      } else setOlderError(describeCode(r.code));
      return;
    }
    setTurns((held) => prependOlder(held, r.value.turns));
    setCursor(r.value.nextCursor);
  }, [loadTurns]);

  const attach = useCallback(
    (runId: string, chatId: string) => {
      detach();
      dispatch({ k: "start", runId });
      const source = new EventSource(runEventsUrl(runId));
      es.current = source;
      let errors = 0;
      const finish = (ev: RunEvent) => {
        source.close();
        if (es.current === source) es.current = null;
        setChats((l) => l.map((c) => (c.id === chatId && c.activeRunId === runId ? { ...c, activeRunId: "" } : c)));
        if (ev.type === "run.completed") {
          // The stored transcript is canonical; the streamed text stays on
          // screen if it cannot be fetched.
          void loadTurns(chatId).then((ok) => {
            if (ok && currentRef.current === chatId) dispatch({ k: "clear" });
            // The server sends the next queued message before it announces the end: follow the run it became.
            void getChat(chatId).then((c) => {
              const next = c.ok ? c.value.activeRunId : "";
              if (!next || next === runId || currentRef.current !== chatId) return;
              const head = queueRef.current?.items[0];
              setChats((l) => l.map((x) => (x.id === chatId ? { ...x, activeRunId: next } : x)));
              if (head) setTurns((t) => [...t, { seq: t.length > 0 ? t[t.length - 1].seq + 1 : 0, role: "user", text: head.input }]);
              attach(next, chatId);
            });
          });
        }
      };
      source.onopen = () => {
        errors = 0;
        attempts.current = 0;
        dispatch({ k: "event", ev: { type: "stream.open" } });
      };
      for (const name of EVENT_NAMES) {
        source.addEventListener(name, (m) => {
          const ev = parseEvent(name, (m as MessageEvent<string>).data);
          if (!ev) return;
          dispatch({ k: "event", ev });
          if (ev.type === "run.completed" || ev.type === "run.failed" || ev.type === "run.cancelled") finish(ev);
          if (ev.type === "run.disconnected") {
            source.close();
            if (es.current === source) es.current = null;
            if (attempts.current < RECONNECT_TRIES) {
              dispatch({ k: "event", ev: { type: "stream.lost" } });
              const wait = 1000 * 2 ** attempts.current++;
              timer.current = window.setTimeout(() => currentRef.current === chatId && attach(runId, chatId), wait);
            }
          }
        });
      }
      for (const name of ["run.prompt", "run.prompt.settled"]) source.addEventListener(name, () => setPromptTick((n) => n + 1));
      source.onerror = () => {
        errors++;
        if (source.readyState === EventSource.CLOSED || errors > RECONNECT_TRIES) {
          // The server refused (or the connection keeps failing). Ask what state
          // the run is really in before saying anything about it.
          source.close();
          if (es.current === source) es.current = null;
          void getRun(runId).then((r) => {
            if (currentRef.current !== chatId) return;
            if (r.ok && r.value.status === "COMPLETED") {
              void loadTurns(chatId).then((ok) => ok && dispatch({ k: "clear" }));
            } else if (r.ok && r.value.status === "CANCELLED") dispatch({ k: "event", ev: { type: "run.cancelled" } });
            else if (r.ok && (r.value.status === "FAILED" || r.value.status === "TIMED_OUT")) {
              dispatch({ k: "event", ev: { type: "run.failed", code: "", status: r.value.status } });
            } else if (r.ok && (r.value.status === "RUNNING" || r.value.status === "PENDING")) {
              dispatch({ k: "event", ev: { type: "stream.closed" } });
            } else dispatch({ k: "event", ev: { type: "run.disconnected" } });
          });
        } else dispatch({ k: "event", ev: { type: "stream.lost" } });
      };
    },
    [detach, loadTurns],
  );

  const open = useCallback(
    (c: ChatSummary) => {
      nav.current++;
      openTitle.current = c.title;
      discardImages(); // staged images belong to the message being written in the chat being left
      detach();
      attempts.current = 0;
      setCurrent(c.id);
      currentRef.current = c.id;
      setTurns([]);
      setCursor("");
      setLoadingOlder(false);
      setOlderError("");
      olderInflight.current = "";
      setTurnsError("");
      setPending(null);
      setBanner("");
      setFocus(null);
      dispatch({ k: "clear" });
      void loadTurns(c.id, "replace");
      if (c.activeRunId) attach(c.activeRunId, c.id);
    },
    [attach, detach, loadTurns],
  );

  // A search result: the owning chat, at the matching turn. The turn may be in an
  // older page than the newest one, so pages are fetched back until it is held.
  const [unlisted, setUnlisted] = useState<ChatSummary | null>(null); // the open chat when it came from search, not the list
  const openResult = useCallback(
    async (r: SearchResult) => {
      let c: ChatSummary | undefined = chats.find((x) => x.id === r.chatId);
      if (!c) {
        const got = await getChat(r.chatId);
        if (!got.ok) {
          setBanner(describeCode(got.code));
          return;
        }
        c = got.value;
        setUnlisted(got.value); // not in the loaded list: kept for the controls that show the open chat's state
      }
      if (r.chatId !== currentRef.current) open(c);
      if (r.turn !== null) setFocus({ chat: r.chatId, seq: r.turn, nonce: ++focusCount.current });
    },
    [chats, open],
  );

  useEffect(() => {
    if (!focus || focus.chat !== current) return;
    if (turnsError) {
      setFocus(null);
      return;
    }
    switch (reachOf(turns, cursor, focus.seq)) {
      case "older":
        if (olderError) {
          // Older pages cannot be fetched, so the turn cannot be reached: say so instead of leaving the click pending.
          setFocus(null);
          setBanner(`Could not reach that message: ${olderError}`);
        } else if (!loadingOlder) void loadOlder();
        break;
      case "gone":
        setFocus(null);
        setBanner("That message is no longer in the conversation.");
        break;
    }
  }, [focus, current, turns, cursor, loadingOlder, olderError, turnsError, loadOlder]);

  useEffect(() => {
    let live = true;
    void listChats().then((r) => {
      if (!live) return;
      if (!r.ok) {
        setListState("error");
        setBanner(describeCode(r.code));
        return;
      }
      setChats(r.value.chats);
      setChatsCursor(r.value.nextCursor);
      setListState("ready");
      if (initialChatId) {
        // A notification link: the server decides whether this chat is the reader's own.
        // Someone else's id and a deleted one both come back NOT_FOUND, and neither opens.
        void getChat(initialChatId).then((c) => {
          if (!live) return;
          if (c.ok) {
            setChats((l) => (l.some((x) => x.id === c.value.id) ? l : [c.value, ...l]));
            open(c.value);
          } else {
            if (r.value.chats.length > 0) open(mostRecent(r.value.chats)); // open() clears the banner, so say it after
            setBanner("That conversation is not available to this account.");
          }
          window.history.replaceState(null, "", "/");
        });
        return;
      }
      if (share !== null) return; // a share lands in the new-chat draft; opening the latest chat would hide it
      if (r.value.chats.length > 0) open(mostRecent(r.value.chats));
    });
    return () => {
      live = false;
    };
    // open is stable; this runs once on mount.
  }, []);

  const moreChats = useCallback(async () => {
    if (!chatsCursor || loadingChats) return;
    setLoadingChats(true);
    const r = await listChats(chatsCursor);
    setLoadingChats(false);
    if (!r.ok) {
      setBanner(describeCode(r.code));
      return;
    }
    setChats((l) => [...l, ...r.value.chats.filter((c) => !l.some((x) => x.id === c.id))]);
    setChatsCursor(r.value.nextCursor);
  }, [chatsCursor, loadingChats]);

  const newChat = useCallback(async (): Promise<ChatSummary | null> => {
    const r = await createChat();
    if (!r.ok) {
      setBanner(describeCode(r.code));
      return null;
    }
    setChats((l) => [r.value, ...l]);
    return r.value;
  }, []);

  // New chat creates the conversation now, so it is listed, selected and real. A second
  // press on a chat that is still blank only returns to its composer: nothing was
  // sent, so a second empty chat would add nothing. A run in the chat being left keeps
  // running on the server; the list remembers it, so opening that chat re-attaches.
  const startChat = useCallback(async () => {
    const focusComposer = () => document.getElementById("composer-input")?.focus();
    if (creating.current) return;
    if (blankChat.current !== "" && blankChat.current === currentRef.current) {
      setBanner("");
      focusComposer();
      return;
    }
    creating.current = true;
    discardImages();
    const at = ++nav.current;
    setBanner("");
    try {
      const c = await newChat();
      if (!c || nav.current !== at) return; // failed (the banner says why), or the reader chose another chat meanwhile
      blankChat.current = c.id;
      detach();
      attempts.current = 0;
      setCurrent(c.id);
      currentRef.current = c.id;
      setTurns([]);
      setCursor("");
      setLoadingOlder(false);
      setOlderError("");
      olderInflight.current = "";
      setTurnsError("");
      setPending(null);
      dispatch({ k: "clear" });
      focusComposer();
    } finally {
      creating.current = false;
    }
  }, [detach, newChat]);

  const submit = useCallback(
    async (input: string, key: string, hold: SendHold, images: AttachmentRef[] = []) => {
      // This submission owns `hold` and ends it exactly once, whichever way it leaves (SendHold.release ignores repeats).
      try {
        setBanner("");
        let chat = current;
        if (!chat) {
          const c = await newChat();
          if (!c) {
            // Nothing was sent and no retry entry exists, so the message goes back into the box (ahead of anything typed
            // since) and into this browser's copy: it is the only place it still lives if autosave had not run yet.
            if (currentRef.current === null && hold.text.trim() !== "") {
              const typed = draftNow.current;
              onDraft(typed.trim() === "" ? hold.text : `${hold.text.replace(/\s*$/, "")}\n\n${typed}`);
            }
            return;
          }
          nav.current++;
          detach();
          setCurrent(c.id);
          currentRef.current = c.id;
          setTurns([]);
          setCursor("");
          chat = c.id;
        }
        blankChat.current = "";
        dispatch({ k: "clear" });
        setPending({ input, key, failure: "", hold, images });
        const r = images.length > 0 ? await sendMessageWithImages(chat, input, key, images.map((i) => i.assetId)) : await sendMessage(chat, input, key);
        // Every outcome ends the hold: admitted deletes the server draft (and the reader's other devices lose it);
        // refused keeps it and resumes syncing whatever is typed next.
        hold.release(r.ok);
        if (currentRef.current !== chat) {
          if (!r.ok && hold.text.trim() !== "" && loadDraft(email, chat) === "") {
            // Refused while the reader was elsewhere: the pending entry is gone, so keep the message in that chat's own
            // draft for when they come back. Never over a draft that has text.
            saveDraft(email, chat, hold.text);
          }
          return;
        }
        if (!r.ok) {
          // Kept with the same key: sending again repeats the request, it cannot double-send.
          setPending({ input, key, failure: images.length > 0 ? describeImageSend(r.code) : describeCode(r.code), hold, images });
          return;
        }
        if (images.length > 0) releaseImages(); // admitted: the strip is done, the staged assets stay for the history thumbnails
        setPending(null);
        setChats((l) => l.map((x) => (x.id === chat ? { ...x, activeRunId: r.value.runId } : x)));
        // A message into an archived chat makes it active on the server; it is not in this list yet, so read the list again.
        if (!chatsRef.current.some((x) => x.id === chat)) {
          setRelist((n) => n + 1);
          setArchiveTick((n) => n + 1);
        }
        setTurns((t) => [...t, { seq: t.length > 0 ? t[t.length - 1].seq + 1 : 0, role: "user", text: input, ...(images.length > 0 ? { attachments: images } : {}) }]);
        attach(r.value.runId, chat);
      } finally {
        hold.release(false); // a path that left without an answer (no-op if already released)
      }
    },
    [attach, current, detach, newChat, email, releaseImages],
  );

  // A disconnected run still occupies the chat: reconnect or cancel it first.
  const busy = pending !== null || (run !== null && !isTerminal(run.phase));

  // Usage is read from the server whenever the chat changes or a reply ends. A failed read shows nothing, never a number.
  const [usage, setUsage] = useState<ChatUsage | null>(null);
  // A reached token limit opens the options once (the notice is in them); after that the toggle owns the state, so they can be closed.
  const budgetReached = usage?.budget.reached === true || usage?.spending?.hardReached === true;
  useEffect(() => {
    if (budgetReached) setMoreOpen(true);
  }, [budgetReached]);
  useEffect(() => {
    setUsage(null);
    if (!current || busy) return;
    let live = true;
    void getUsage(current).then((r) => live && setUsage(r.ok ? r.value : null));
    return () => {
      live = false;
    };
  }, [current, busy]);

  // The model picker is offered only when discovery confirms models (capability "models"); a failed
  // or unreadable discovery offers nothing.
  // An unreadable discovery is not "unsupported": it says so and can be asked again, so one transient
  // failure does not take the picker away for the rest of the session.
  const [inventory, setInventory] = useState<Inventory | null>(null);
  const [inventoryFailed, setInventoryFailed] = useState(false);
  const [inventoryTick, setInventoryTick] = useState(0);
  // Deleting a conversation removes the assistant's session too, so it is offered only when the assistant says it can.
  const deleteBlocked = offers(inventory, "chat_delete")
    ? ""
    : inventory
      ? inventory.capabilities.find((c) => c.id === "chat_delete")?.reason || "The assistant does not offer deleting conversations."
      : inventoryFailed
        ? "What the assistant supports could not be read, so deleting is off."
        : "Checking what the assistant supports…";
  const listArchive = useCallback((cursor?: string) => listChats(cursor, true), []);

  // A chat that was deleted or archived somewhere else is gone from the server's list: read it again.
  const reloadChats = useCallback(async () => {
    const r = await listChats();
    if (!r.ok) return;
    setChats(r.value.chats);
    setChatsCursor(r.value.nextCursor);
  }, []);

  useEffect(() => {
    if (relist > 0) void reloadChats();
  }, [relist, reloadChats]);

  // Leave the open chat without opening another (it was deleted and nothing else is listed).
  const leaveChat = useCallback(() => {
    nav.current++;
    discardImages();
    detach();
    attempts.current = 0;
    blankChat.current = "";
    setCurrent(null);
    currentRef.current = null;
    setTurns([]);
    setCursor("");
    setLoadingOlder(false);
    setOlderError("");
    olderInflight.current = "";
    setTurnsError("");
    setPending(null);
    setFocus(null);
    dispatch({ k: "clear" });
  }, [detach, discardImages]);

  const chatActions: ChatActions = useMemo(() => {
    const failed = async (code: string, what: "change" | "delete") => {
      if (code === "NOT_FOUND") void reloadChats(); // already gone elsewhere: the list should say so
      return describeManage(code, what);
    };
    return {
      rename: async (c, title) => {
        const r = await changeChat(c.id, { title });
        if (!r.ok) return failed(r.code, "change");
        setChats((l) => l.map((x) => (x.id === c.id ? { ...x, title: r.value.title } : x)));
        if (currentRef.current === c.id) openTitle.current = r.value.title;
        setSearchEpoch((n) => n + 1);
        setArchiveTick((n) => n + 1);
        return "";
      },
      pin: async (c, pinned) => {
        const r = await changeChat(c.id, { pinned });
        if (!r.ok) return failed(r.code, "change");
        await reloadChats(); // the server decides the order
        return "";
      },
      archive: async (c, archived) => {
        const r = await changeChat(c.id, { archived });
        if (!r.ok) return failed(r.code, "change");
        if (archived) setChats((l) => l.filter((x) => x.id !== c.id));
        else await reloadChats();
        setArchiveTick((n) => n + 1);
        return "";
      },
      remove: async (c) => {
        const r = await deleteChat(c.id);
        // Only the server's word removes anything here. NOT_FOUND is the same word: it is already gone.
        if (!r.ok && r.code !== "NOT_FOUND") {
          void reloadChats(); // a failed delete leaves the chat flagged, and the list shows it
          setArchiveTick((n) => n + 1);
          return describeManage(r.code, "delete");
        }
        forgetChatDrafts(email, c.id);
        const rest = chats.filter((x) => x.id !== c.id);
        setChats(rest);
        setSearchEpoch((n) => n + 1);
        setArchiveTick((n) => n + 1);
        if (currentRef.current === c.id) {
          const next = rest.length > 0 ? mostRecent(rest) : null;
          if (next) open(next);
          else leaveChat();
        }
        return "";
      },
    };
  }, [chats, email, leaveChat, open, reloadChats]);
  useEffect(() => {
    const ctl = new AbortController();
    void getCapabilities(ctl.signal).then((r) => {
      if (ctl.signal.aborted) return;
      setInventory(r.ok ? r.value : null);
      setInventoryFailed(!r.ok);
    });
    return () => ctl.abort();
  }, [inventoryTick]);

  // Whether the next message may carry images is the server's answer for this chat's model (from the
  // model catalogue), read again when the chat, its model or its run state changes. Unknown is "off".
  const [imageInput, setImageInput] = useState<ImageInput | null>(null);
  const [modelTick, setModelTick] = useState(0);
  useEffect(() => setImageInput(null), [current]);
  useEffect(() => {
    if (!current || busy) return;
    const ctl = new AbortController();
    void getImageInput(current, ctl.signal).then((r) => !ctl.signal.aborted && setImageInput(r.ok ? r.value : null));
    return () => ctl.abort();
  }, [current, busy, modelTick]);

  const onSubmit = () => {
    const input = draft.trim();
    setSteer(null); // a new message starts a new run; the last steer's outcome no longer describes it
    const hold = sync.hold();
    setDraft("");
    saveDraft(email, currentRef.current, "");
    const refs: AttachmentRef[] = att.items.filter((a) => a.status === "ready" && a.assetId).map((a) => ({ assetId: a.assetId as string, mediaType: a.file.type, available: true }));
    void submit(input, newIdempotencyKey(), hold, refs);
  };

  const onCancel = () => {
    if (!run) return;
    void cancelRun(run.runId).then((r) => {
      if (!r.ok) {
        setBanner(describeCode(r.code));
        return;
      }
      if (r.value.status === "CANCELLED") {
        detach();
        dispatch({ k: "event", ev: { type: "run.cancelled" } });
        if (!r.value.upstreamStopped) dispatch({ k: "maybe" });
      }
      // Any other status means the run ended on its own first: the stream reports it.
    });
  };

  // Steer goes to the live run only; it never becomes a user turn. The draft is cleared only when the assistant accepted it
  // and the text was not changed meanwhile; a refusal keeps it, and a refusal from a run that had ended is called too late.
  // The outcome belongs to the chat it was sent from, so it stays on screen after the run ends, until the next send.
  const [steer, setSteer] = useState<{ chatId: string | null; note: SteerNote; failure: string } | null>(null);
  const canSteer = run !== null && run.phase === "streaming" && offers(inventory, "steer");
  const onSteer = () => {
    const text = draft.trim();
    if (!run || !canSteer || text === "") return;
    const { runId } = run;
    const chat = currentRef.current;
    const note = (n: SteerNote, failure = "") => setSteer({ chatId: chat, note: n, failure });
    note("sending");
    void steerRun(runId, text).then(async (r) => {
      if (r.ok) {
        note("accepted");
        if (currentRef.current === chat && draftRef.current.trim() === text) {
          setDraft("");
          saveDraft(email, chat, "");
        }
        return;
      }
      if (r.code !== "STEER_REFUSED") {
        note("failed", describeCode(r.code));
        return;
      }
      const now = await getRun(runId);
      const over = now.ok && ["COMPLETED", "FAILED", "CANCELLED", "TIMED_OUT"].includes(now.value.status);
      note(over ? "late" : "refused");
    });
  };

  const steerShown = steer !== null && steer.chatId === current;

  // Queue: the text waits on the server and is sent as a new message after the reply ends. The draft is cleared only
  // when the server stored it and the text was not changed meanwhile; a refusal keeps it and says why.
  const queueRef = useRef<ChatQueue | null>(null);
  const [queueTick, setQueueTick] = useState(0);
  const [queueFailure, setQueueFailure] = useState("");
  useEffect(() => setQueueFailure(""), [current]);
  const onQueue = () => {
    const text = draft.trim();
    const chat = currentRef.current;
    if (!chat || text === "") return;
    void enqueueMessage(chat, text).then((r) => {
      if (currentRef.current !== chat) return;
      if (!r.ok) {
        setQueueFailure(describeCode(r.code));
        return;
      }
      setQueueFailure("");
      queueRef.current = r.value;
      setQueueTick((n) => n + 1);
      // The reply ended while this was on its way, so the server sent it at once: follow that run.
      if (r.value.started) onQueueSent(r.value.started.runId, r.value.started.input);
      if (draftRef.current.trim() === text) {
        setDraft("");
        saveDraft(email, chat, "");
      }
    });
  };
  const onQueueSent = (runId: string, input: string) => {
    const chat = currentRef.current;
    if (!chat) return;
    setChats((l) => l.map((x) => (x.id === chat ? { ...x, activeRunId: runId } : x)));
    setTurns((t) => [...t, { seq: t.length > 0 ? t[t.length - 1].seq + 1 : 0, role: "user", text: input }]);
    attach(runId, chat);
  };

  // Nothing to read yet: the conversation area shows the starting card instead of an empty list.
  const empty = turns.length === 0 && !pending && !run && !turnsError && (current === null || current === blankChat.current);
  return (
    <div className="chat-app">
      <header
        className="bar"
        onKeyDown={(e) => {
          if (e.key === "Escape" && menuOpen) setMenuOpen(false);
        }}
      >
        <strong className="brand">
          <AntaresMark size={20} />
          Antares
        </strong>
        <div className="account">
          <span className="who-am-i">{email}</span>
          <button type="button" className="quiet bar-toggle" data-icon="chevron" aria-expanded={menuOpen} aria-controls="bar-actions" onClick={() => setMenuOpen((o) => !o)}>
            Menu
          </button>
          <div id="bar-actions" className={menuOpen ? "bar-actions open" : "bar-actions"}>
            <Notifications />
            <MemorySkillsPanel />
            <UsageDashboard />
            {offers(inventory, "jobs") && <JobsPanel />}
            {current && current !== blankChat.current && <ExportChat chatId={current} title={chats.find((c) => c.id === current)?.title ?? openTitle.current} />}
            {canAdmin && onAdmin && (
              <button type="button" className="quiet" data-icon="shield" onClick={onAdmin}>
                Admin
              </button>
            )}
            <AppearancePanel />
            <DiagnosticsPanel />
            <button type="button" className="quiet" data-icon="logout" onClick={() => onSignOut()}>
              Sign out
            </button>
            {/* #150: ends this account's sessions on every device; each one has to sign in again. */}
            <button type="button" className="quiet" data-icon="logout" onClick={() => onSignOut(true)}>
              Sign out everywhere
            </button>
            {signOutError && <p role="alert">{signOutError}</p>}
          </div>
        </div>
      </header>
      <nav className="side" aria-label="Conversations">
        <button type="button" data-icon="plus" onClick={() => void startChat()}>
          New chat
        </button>
        <SearchPanel key={searchEpoch} current={current} onOpen={(r) => void openResult(r)} />
        {listState === "loading" && <p className="label">Loading…</p>}
        {listState === "ready" && chats.length === 0 && <p className="label">No conversations yet.</p>}
        <ChatList
          chats={chats}
          current={current}
          onOpen={open}
          actions={chatActions}
          deleteBlocked={deleteBlocked}
          listArchive={listArchive}
          archiveTick={archiveTick}
          slot={actionsSlot}
          more={
            chatsCursor && (
              <button type="button" className="quiet" disabled={loadingChats} onClick={() => void moreChats()}>
                {loadingChats ? "Loading…" : "Show more"}
              </button>
            )
          }
        />
      </nav>
      <main className={empty ? "pane empty" : "pane"}>
        {banner && (
          <p className="notice" role="alert">
            {banner}
          </p>
        )}
        {turnsError && (
          <p className="notice" role="alert">
            History unavailable: {turnsError}
          </p>
        )}
        {empty && (
          <div className="hero">
            <span className="hero-tag" aria-hidden="true">
              New chat
            </span>
            <p className="hero-text">{current === null ? "Start a conversation below." : "New conversation. Write a message below."}</p>
          </div>
        )}
        <ReplyContext.Provider value={addToDraft}>
        <Transcript
          key={current ?? "new"}
          chatId={current ?? undefined}
          inventory={inventory}
          onReuse={reuseText}
          turns={turns}
          pending={pending}
          run={run}
          promptTick={promptTick}
          hasMore={cursor !== ""}
          loadingMore={loadingOlder}
          olderError={olderError}
          onLoadMore={() => void loadOlder()}
          focusNonce={focus?.nonce ?? 0}
          focusSeq={focus && focus.chat === current && reachOf(turns, cursor, focus.seq) === "held" ? focus.seq : null}
          onCancel={onCancel}
          onRetry={() => {
            // Text and images come from the same turn of this chat (see retry.ts); never from state left by another send.
            const plan = planRetry(turns);
            if (plan.kind === "send") void submit(plan.text, newIdempotencyKey(), NO_HOLD, plan.images); // a re-ask of an earlier message, not the draft
            else if (plan.kind === "redraft") {
              onDraft(draft.trim() === "" ? plan.text : `${draft.replace(/\s*$/, "")}\n\n${plan.text}`);
              setBanner(plan.why);
            }
          }}
          onReconnect={() => {
            if (run && current) {
              attempts.current = 0;
              attach(run.runId, current);
            }
          }}
          onResend={() => {
            if (!pending) return;
            // A resend gets its own hold on the same draft (the first was released when the send was refused).
            void submit(pending.input, pending.key, pending.hold.again(), pending.images ?? []);
          }}
        />
        </ReplyContext.Provider>
        {/* Beside the input (#164): the chat's model and effort, one open away from changing, and usage at a glance. */}
        <div className="composer-chips">
          <button type="button" className="quiet extras-toggle" data-icon="chevron" aria-expanded={moreOpen} aria-controls="chat-extras" onClick={() => setMoreOpen((o) => !o)}>
            {budgetReached && !moreOpen ? "Options · token limit reached" : "Options"}
          </button>
          {current && offers(inventory, "models") && <ModelPicker chatId={current} disabled={busy} onChanged={() => setModelTick((n) => n + 1)} />}
          {!current && offers(inventory, "models") && <span className="label">Model: assistant default (choose one once the chat exists)</span>}
          {current && inventoryFailed && (
            <p role="status">
              Could not check which models are available.{" "}
              <button type="button" onClick={() => setInventoryTick((n) => n + 1)}>Retry model check</button>
            </p>
          )}
          {usage && (
            <button type="button" className="quiet" data-chip="usage" aria-controls="chat-extras" aria-expanded={moreOpen} onClick={() => setMoreOpen((o) => !o)}>
              {describeGlance(usage)}
            </button>
          )}
          {usage?.spending && (usage.spending.warnings.length > 0 || usage.spending.hardReached) && (
            <span className="sr" role="status">
              {usage.spending.hardReached ? "The daily spending limit is reached: new messages are refused." : `Spending warning: the ${usage.spending.warnings.join(" and ")} warning is reached. Messages still go through.`}
            </span>
          )}
        </div>
        <div id="chat-extras" className={moreOpen ? "extras open" : "extras"}>
          <div className="chat-actions-slot" ref={setActionsSlot} />
          {usage && (
            <details className="usage" open={usage.budget.reached}>
              <summary>{usage.budget.reached ? "Token limit reached" : usage.spending?.hardReached ? "Daily spending limit reached" : usage.spending?.warnings.length ? "Usage: spending warning reached" : usage.tokens ? `${usage.tokens.total.toLocaleString("en-US")} tokens used` : "Usage unknown"}</summary>
              <ul>
                {[...describeUsage(usage), ...(usage.spending ? describeSpending(usage.spending) : [])].map((l) => (
                  <li key={l}>{l}</li>
                ))}
              </ul>
              {usage.turns.length > 0 && (
                <details>
                  <summary>Per reply{usage.turnsTruncated ? " (newest 100)" : ""}</summary>
                  <ul>
                    {describeTurns(usage).map((l) => (
                      <li key={l}>{l}</li>
                    ))}
                  </ul>
                </details>
              )}
              <BudgetForm
                limit={usage.budget.limit}
                onSaved={() => current && void getUsage(current).then((r) => setUsage(r.ok ? r.value : null))}
              />
              {usage.spending && (
                <SpendingForm spending={usage.spending} onSaved={() => current && void getUsage(current).then((r) => setUsage(r.ok ? r.value : null))} />
              )}
            </details>
          )}
          {current && current !== blankChat.current && (
            <OpenChatVisualize
              chat={chats.find((c) => c.id === current) ?? (unlisted?.id === current ? unlisted : undefined)}
              onChanged={(c) => {
                setChats((l) => l.map((x) => (x.id === c.id ? { ...x, visualize: c.visualize } : x)));
                setUnlisted((u) => (u?.id === c.id ? { ...u, visualize: c.visualize } : u));
              }}
            />
          )}
          <Dictation onText={(t) => onDraft(draft.trim() === "" ? t : `${draft.replace(/\s*$/, "")} ${t}`)} />
          <Recorder />
          <Templates own={ownTemplates} onChange={setOwnTemplates} />
          <p className="label" data-policy="drafts">
            Unsent text is kept on this device until you sign out or your session ends, and text older than 7 days is cleared the next time you open Antares. Your account keeps a copy for your other devices.
          </p>
        </div>
        {current && current !== blankChat.current && (
          <QueuePanel chatId={current} busy={busy} tick={queueTick} onSent={onQueueSent} onQueue={(q) => (queueRef.current = q)} />
        )}
        <DraftNotice sync={sync} />
        <Composer
          value={draft}
          busy={busy}
          onChange={onDraft}
          onSubmit={onSubmit}
          queue={busy && current && run !== null && !isTerminal(run.phase) ? { failure: queueFailure, onQueue } : undefined}
          steer={canSteer || steerShown ? { available: canSteer, note: steerShown ? steer.note : null, failure: steerShown ? steer.failure : "", onSteer } : undefined}
          templates={mergeTemplates(ownTemplates)}
          images={{
            available: imageInput?.available === true,
            reason: imageInput ? imageInput.reason : current ? "CATALOGUE_UNAVAILABLE" : "NO_CHAT",
            items: att.items,
            notice: att.notice,
            pending: att.pending,
            onFiles: att.add,
            onRemove: att.remove,
            onRetry: att.retry,
          }}
        />
      </main>
    </div>
  );
}

/** Optional money limits (#152): two warnings and one hard limit, in the price list's currency. Empty means none. */
function SpendingForm({ spending, onSaved }: { spending: Spending; onSaved: () => void }) {
  const server = spendingFields(spending);
  const [v, setV] = useState(server);
  const base = useRef(server);
  const key = `${server.dailySoft}|${server.monthlySoft}|${server.dailyHard}`;
  useEffect(() => {
    const before = base.current;
    base.current = server;
    setV((typed) => reconcileSpending(before, server, typed));
  }, [key]);
  const [err, setErr] = useState("");
  const read = parseRupiah;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const l = { dailySoft: read(v.dailySoft), monthlySoft: read(v.monthlySoft), dailyHard: read(v.dailyHard) };
        if (l.dailySoft === undefined || l.monthlySoft === undefined || l.dailyHard === undefined) {
          setErr("Use whole rupiah amounts, or leave a field empty for no limit. Nothing was changed.");
          return;
        }
        void putSpending(l as { dailySoft: number | null; monthlySoft: number | null; dailyHard: number | null }).then((r) => {
          setErr(r.ok ? "" : "Could not save the limits. Nothing was changed.");
          if (r.ok) onSaved();
        });
      }}
    >
      <label>
        Daily warning (Rp, a warning only) <input inputMode="numeric" value={v.dailySoft} onChange={(e) => setV({ ...v, dailySoft: e.target.value })} />
      </label>{" "}
      <label>
        Monthly warning (Rp, a warning only) <input inputMode="numeric" value={v.monthlySoft} onChange={(e) => setV({ ...v, monthlySoft: e.target.value })} />
      </label>{" "}
      <label>
        Daily spending limit (Rp, refuses new messages) <input inputMode="numeric" value={v.dailyHard} onChange={(e) => setV({ ...v, dailyHard: e.target.value })} />
      </label>{" "}
      <button type="submit">Save spending limits</button>
      {err && <p role="alert">{err}</p>}
    </form>
  );
}

/** Opt-in per-chat token limit. Empty means no limit; saving re-reads the usage so the stated state is the server's. */
function BudgetForm({ limit, onSaved }: { limit: number | null; onSaved: () => void }) {
  const [text, setText] = useState(limit === null ? "" : String(limit));
  const [err, setErr] = useState("");
  useEffect(() => setText(limit === null ? "" : String(limit)), [limit]);
  const save = async (value: number | null) => {
    const r = await putBudget(value);
    setErr(r.ok ? "" : "Could not save the limit. Use a whole number from 10,000 to 1,000,000,000.");
    if (r.ok) onSaved();
  };
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const v = parseLimitInput(text);
        if (v === undefined) {
          setErr("Enter a whole number from 10,000 to 1,000,000,000, or leave it empty for no limit. Nothing was changed.");
          return;
        }
        void save(v);
      }}
    >
      <label>
        Token limit across all your chats (empty = none){" "}
        <input inputMode="numeric" value={text} onChange={(e) => setText(e.target.value)} />
      </label>{" "}
      <button type="submit">Save limit</button>
      {limit !== null && (
        <button type="button" onClick={() => void save(null)}>
          Remove limit
        </button>
      )}
      {err && <p role="alert">{err}</p>}
    </form>
  );
}

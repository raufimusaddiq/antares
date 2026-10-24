import { useCallback, useEffect, useLayoutEffect, useReducer, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import {
  cancelRun,
  createChat,
  describeCode,
  describeTurns,
  describeUsage,
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
import { MessageBody } from "./MessageBody";
import { MAX_REPLY, ReplyContext } from "./openui/reply";
import { Artifacts } from "./Artifacts";
import { ExportChat } from "./ExportChat";
import { indentInFence, wrapSelection, type Edit } from "./composerEdit";
import { Notifications } from "./Notifications";
import { VirtualList } from "./VirtualList";
import { EVENT_NAMES, isTerminal, newRun, parseEvent, reduce, runNotice, type LiveRun, type RunEvent } from "./runState";
import { AntaresMark } from "./brand/AntaresMark";
import { Recorder } from "./Recorder";
import { loadDraft, matchTemplates, mergeTemplates, saveDraft, type Template } from "./drafts";
import { DraftNotice, takeSharedText, useDraftSync } from "./DraftSync";
import { listOwnTemplates, type OwnTemplate } from "./draftApi";
import { Templates } from "./Templates";

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
}

// ---- presentational pieces (no hooks: rendered to static markup in tests) -----

export function RunView({ run, onCancel, onRetry, onReconnect }: { run: LiveRun; onCancel: () => void; onRetry: () => void; onReconnect: () => void }) {
  const notice = runNotice(run);
  const live = run.phase === "streaming" || run.phase === "reconnecting";
  const disconnected = run.phase === "disconnected" || run.phase === "detached";
  return (
    <article className={`turn assistant run-${run.phase}`} aria-label="Assistant reply">
      <h2 className="who">Assistant</h2>
      {run.tools.length > 0 && (
        <ul className="tools" aria-label="Tool activity">
          {run.tools.map((t, i) => (
            <li key={`${t.id}-${i}`}>
              {t.label || "Tool"} — {t.done ? (t.summary ? `done: ${t.summary}` : "done") : "running"}
            </li>
          ))}
        </ul>
      )}
      {run.text !== "" && (
        <div className={run.phase === "completed" ? "" : "partial"}>
          {run.phase !== "completed" && !live && <p className="label">Partial response</p>}
          <MessageBody text={run.text} streaming={live} />
        </div>
      )}
      {live && run.text === "" && <p className="label">Working…</p>}
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

const turnKey = (t: Turn) => String(t.seq);

export interface TranscriptProps {
  turns: Turn[];
  pending: Pending | null;
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
  onCancel: () => void;
  onRetry: () => void;
  onReconnect: () => void;
  onResend: () => void;
}

const never = () => {};

export function Transcript(props: TranscriptProps) {
  const { turns, pending, run, hasMore = false, loadingMore = false, olderError = "", onLoadMore = never, focusSeq = null, focusNonce = 0, chatId } = props;
  const renderTurn = useCallback(
    (t: Turn) => (
      <article className={`turn ${t.role}`} data-found={t.seq === focusSeq ? "true" : undefined} aria-current={t.seq === focusSeq ? "location" : undefined}>
        <h2 className="who">{t.role === "user" ? "You" : "Assistant"}</h2>
        {t.role === "user" ? <p className="plain">{t.text}</p> : <MessageBody text={t.text} streaming={false} />}
        {t.role === "assistant" && chatId && t.ref && <Artifacts chatId={chatId} seq={t.seq} turnRef={t.ref} text={t.text} />}
      </article>
    ),
    [focusSeq, chatId],
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
          <p className="plain">{pending.input}</p>
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

export function Composer(props: { value: string; busy: boolean; onChange: (v: string) => void; onSubmit: () => void; templates?: readonly Template[] }) {
  const empty = props.value.trim() === "";
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
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    const t = e.currentTarget;
    if ((IS_MAC ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && !e.altKey && !e.shiftKey && !e.nativeEvent.isComposing) {
      apply(e, wrapSelection(t.value, t.selectionStart, t.selectionEnd, e.key));
    } else if (e.key === "Tab" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && !e.nativeEvent.isComposing) {
      apply(e, indentInFence(t.value, t.selectionStart, t.selectionEnd));
    }
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      if (matches.length > 0) insert(matches[0]); // an open template menu: Enter picks, it does not send "/sum"
      else if (!empty && !props.busy) props.onSubmit();
    }
  };
  return (
    <form
      className="composer"
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        if (!empty && !props.busy) props.onSubmit();
      }}
    >
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
        onKeyDown={onKey}
      />
      <button type="submit" disabled={empty || props.busy}>
        Send
        <svg className="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
          <path d="M5 12h14M13 6l6 6-6 6" />
        </svg>
      </button>
    </form>
  );
}

// ---- the app --------------------------------------------------------------------

export function Chat({ email, onSignOut, canAdmin = false, onAdmin, initialChatId = null }: { email: string; onSignOut: () => void; canAdmin?: boolean; onAdmin?: () => void; initialChatId?: string | null }) {
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
  const [run, dispatch] = useReducer(runReducer, null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [draft, setDraft] = useState("");
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
  const [banner, setBanner] = useState("");
  const [focus, setFocus] = useState<{ chat: string; seq: number; nonce: number } | null>(null); // a search result being opened at its turn

  useEffect(() => setDraft(loadDraft(email, current)), [email, current]);
  // Text the OS shared to the app: it waits in the new-chat draft, never in a chat and never sent.
  const [share, setShare] = useState<string | null>(takeSharedText);
  const sync = useDraftSync(email, current, draft, onDraft, share, () => setShare(null));
  const [ownTemplates, setOwnTemplates] = useState<OwnTemplate[]>([]);
  useEffect(() => {
    void listOwnTemplates().then((l) => l && setOwnTemplates(l));
  }, []);

  const es = useRef<EventSource | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const attempts = useRef(0);
  const currentRef = useRef<string | null>(null);
  const lastInput = useRef("");
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
            if (r.value.chats.length > 0) open(r.value.chats[0]); // open() clears the banner, so say it after
            setBanner("That conversation is not available to this account.");
          }
          window.history.replaceState(null, "", "/");
        });
        return;
      }
      if (share !== null) return; // a share lands in the new-chat draft; opening the latest chat would hide it
      if (r.value.chats.length > 0) open(r.value.chats[0]);
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
    async (input: string, key: string) => {
      setBanner("");
      let chat = current;
      if (!chat) {
        const c = await newChat();
        if (!c) return;
        nav.current++;
        detach();
        setCurrent(c.id);
        currentRef.current = c.id;
        setTurns([]);
        setCursor("");
        chat = c.id;
      }
      blankChat.current = "";
      lastInput.current = input;
      dispatch({ k: "clear" });
      setPending({ input, key, failure: "" });
      const r = await sendMessage(chat, input, key);
      if (currentRef.current !== chat) return;
      if (!r.ok) {
        // Kept with the same key: sending again repeats the request, it cannot double-send.
        setPending({ input, key, failure: describeCode(r.code) });
        return;
      }
      setPending(null);
      setChats((l) => l.map((x) => (x.id === chat ? { ...x, activeRunId: r.value.runId } : x)));
      setTurns((t) => [...t, { seq: t.length > 0 ? t[t.length - 1].seq + 1 : 0, role: "user", text: input }]);
      attach(r.value.runId, chat);
    },
    [attach, current, detach, newChat],
  );

  // A disconnected run still occupies the chat: reconnect or cancel it first.
  const busy = pending !== null || (run !== null && !isTerminal(run.phase));

  // Usage is read from the server whenever the chat changes or a reply ends. A failed read shows nothing, never a number.
  const [usage, setUsage] = useState<ChatUsage | null>(null);
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
  const [inventory, setInventory] = useState<Inventory | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    void getCapabilities(ctl.signal).then((r) => !ctl.signal.aborted && setInventory(r.ok ? r.value : null));
    return () => ctl.abort();
  }, []);

  const onSubmit = () => {
    const input = draft.trim();
    setDraft("");
    saveDraft(email, currentRef.current, "");
    void submit(input, newIdempotencyKey());
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

  return (
    <div className="chat-app">
      <header className="bar">
        <strong className="brand">
          <AntaresMark size={20} />
          Antares
        </strong>
        <span className="who-am-i">{email}</span>
        <Notifications />
        {current && current !== blankChat.current && <ExportChat chatId={current} title={chats.find((c) => c.id === current)?.title ?? ""} />}
        {canAdmin && onAdmin && (
          <button type="button" className="quiet" onClick={onAdmin}>
            Admin
          </button>
        )}
        <button type="button" className="quiet" onClick={onSignOut}>
          Sign out
        </button>
      </header>
      <nav className="side" aria-label="Conversations">
        <button type="button" onClick={() => void startChat()}>
          New chat
        </button>
        <SearchPanel current={current} onOpen={(r) => void openResult(r)} />
        {listState === "loading" && <p className="label">Loading…</p>}
        {listState === "ready" && chats.length === 0 && <p className="label">No conversations yet.</p>}
        <ul>
          {chats.map((c) => (
            <li key={c.id}>
              <button type="button" className={c.id === current ? "item current" : "item"} aria-current={c.id === current ? "true" : undefined} onClick={() => open(c)}>
                {c.title || "New chat"}
              </button>
            </li>
          ))}
        </ul>
        {chatsCursor && (
          <button type="button" className="quiet" disabled={loadingChats} onClick={() => void moreChats()}>
            {loadingChats ? "Loading…" : "Show more"}
          </button>
        )}
      </nav>
      <main className="pane">
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
        {turns.length === 0 && !pending && !run && !turnsError && (current === null || current === blankChat.current) && (
          <p className="label">{current === null ? "Start a conversation below." : "New conversation. Write a message below."}</p>
        )}
        <ReplyContext.Provider value={addToDraft}>
        <Transcript
          key={current ?? "new"}
          chatId={current ?? undefined}
          turns={turns}
          pending={pending}
          run={run}
          hasMore={cursor !== ""}
          loadingMore={loadingOlder}
          olderError={olderError}
          onLoadMore={() => void loadOlder()}
          focusNonce={focus?.nonce ?? 0}
          focusSeq={focus && focus.chat === current && reachOf(turns, cursor, focus.seq) === "held" ? focus.seq : null}
          onCancel={onCancel}
          onRetry={() => {
            const last = [...turns].reverse().find((t) => t.role === "user");
            const input = lastInput.current || last?.text || "";
            if (input) void submit(input, newIdempotencyKey());
          }}
          onReconnect={() => {
            if (run && current) {
              attempts.current = 0;
              attach(run.runId, current);
            }
          }}
          onResend={() => pending && void submit(pending.input, pending.key)}
        />
        </ReplyContext.Provider>
        {usage && (
          <details className="usage" open={usage.budget.reached}>
            <summary>{usage.budget.reached ? "Token limit reached" : usage.tokens ? `${usage.tokens.total.toLocaleString("en-US")} tokens` : "Usage unknown"}</summary>
            <ul>
              {describeUsage(usage).map((l) => (
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
          </details>
        )}
        {current && offers(inventory, "models") && <ModelPicker chatId={current} disabled={busy} />}
        <Recorder />
        <DraftNotice sync={sync} />
        <Templates own={ownTemplates} onChange={setOwnTemplates} />
        <Composer value={draft} busy={busy} onChange={onDraft} onSubmit={onSubmit} templates={mergeTemplates(ownTemplates)} />
      </main>
    </div>
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

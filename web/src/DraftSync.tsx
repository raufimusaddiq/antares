import { useCallback, useEffect, useRef, useState } from "react";
import { loadBaseRevision, loadDraft, loadCleared, saveBaseRevision, saveCleared } from "./drafts";
import { draftBytes, MAX_DRAFT_BYTES, readDraft, removeDraft, sharedText, writeDraft } from "./draftApi";

// Keeps the server copy of the draft on screen in step with the text box (PRD §21.6).
// The browser copy in drafts.ts stays the instant path; this adds the other devices.
// A write names the revision it last saw. If the server moved on, nothing is overwritten:
// syncing pauses and the reader picks which text to keep.

const DEBOUNCE_MS = 800;
const SHARE_WAIT_MS = 5000; // how long a share waits for the first draft read before using this browser's copy

const withShare = (base: string, share: string) => (base.trim() === "" ? share : `${base.replace(/\s*$/, "")}\n\n${share}`);

interface Slot {
  chatId: string | null;
  rev: number;
  synced: string; // the text the server holds at `rev`
  latest: string;
  loaded: boolean;
  loading: boolean;
  attempted: boolean; // a read has finished, successfully or not
  paused: boolean;
  busy: boolean;
  stale: string | null; // the previous chat's text still in the box until the composer resets; never ours
  box: string; // what the composer holds for this slot right now, tracked even while the slot is held
  held: boolean; // a send is in flight: the server draft stays until the send is admitted
  shareText: string | null; // the OS share already added to the box: any conflict candidate built later must carry it too
  shared: boolean; // the OS share has been added to this slot's draft
}

export interface DraftSyncView {
  /** The other device's text, while the two disagree. Syncing is paused until one is chosen. */
  conflict: { theirs: string } | null;
  /** The server copy is unreachable: the draft survives in this browser only. */
  offline: boolean;
  /** The server (or the size bound) refused this text. Retrying the same text cannot help; editing it can. */
  refusal: string | null;
  /** Try the server again after a failed read. */
  retry: () => void;
  /**
   * Call when a send starts, before the box is emptied. The server draft is left alone (the same draft is on the
   * reader's other devices) while the send is in flight. Call the returned function on EVERY outcome: with true
   * once the send is admitted (the draft is deleted, or whatever was typed meanwhile is saved), with false when
   * it was refused or never started (the server draft stays, and syncing of new text resumes at once).
   */
  hold: () => (admitted: boolean) => void;
  keepMine: () => void;
  useTheirs: () => void;
}

/**
 * chatId null is the new-chat draft. `share` is text the OS handed to the app; it is appended
 * to the new-chat draft once that draft has loaded, and never sent.
 */
export function useDraftSync(email: string, chatId: string | null, draft: string, setDraft: (v: string) => void, share: string | null, onShared: () => void): DraftSyncView {
  const slot = useRef<Slot | null>(null);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const timer = useRef<number | undefined>(undefined);
  const [conflict, setConflict] = useState<{ theirs: string } | null>(null);
  const [offline, setOffline] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  const onSharedRef = useRef(onShared);
  onSharedRef.current = onShared;
  const setDraftRef = useRef(setDraft);
  setDraftRef.current = setDraft;

  const flush = useCallback(async (s: Slot): Promise<void> => {
    if (!s.loaded || s.paused || s.busy || s.held || s.latest === s.synced) return;
    const text = s.latest;
    const bytes = draftBytes(text);
    if (bytes > MAX_DRAFT_BYTES) {
      // Refused here, before sending, with the real reason: the bound is bytes, and the composer counts characters.
      if (slot.current === s) setRefusal(`This draft is ${bytes.toLocaleString("en-US")} bytes and the server keeps at most ${MAX_DRAFT_BYTES.toLocaleString("en-US")} bytes (about ${Math.floor(MAX_DRAFT_BYTES / 3).toLocaleString("en-US")} CJK characters). It stays in this browser only; shorten it to sync.`);
      return;
    }
    if (slot.current === s) setRefusal(null);
    s.busy = true;
    const r = text === "" ? (s.rev === 0 ? { kind: "ok", revision: 0 } as const : await removeDraft(s.chatId, s.rev)) : await writeDraft(s.chatId, text, s.rev);
    s.busy = false;
    if (r.kind === "ok") {
      s.rev = r.revision;
      s.synced = text;
      saveBaseRevision(email, s.chatId, s.rev);
      saveCleared(email, s.chatId, false);
      if (slot.current === s) {
        setOffline(false);
        setRefusal(null);
      }
    } else if (r.kind === "refused") {
      if (slot.current === s) setRefusal(r.reason);
      return;
    } else if (r.kind === "conflict") {
      s.rev = r.revision;
      s.synced = r.body; // what the server really holds now, so a later edit back to the old text is still a change
      s.paused = true;
      if (slot.current === s) setConflict({ theirs: s.shareText === null ? r.body : withShare(r.body, s.shareText) });
      return;
    } else {
      if (slot.current === s) setOffline(true);
      return;
    }
    if (s.latest !== s.synced) void flush(s); // typed while the write was in flight
  }, [email]);

  // Read the server copy and reconcile it with what is in the box. A failed read leaves the slot
  // unloaded (nothing is written) and is retried on reconnect, on the Retry button and on the
  // next edit; the local text is kept, and the normal conflict check runs when a read succeeds.
  const load = useCallback(
    async (s: Slot): Promise<void> => {
      if (s.loaded || s.loading) return;
      s.loading = true;
      const r = await readDraft(s.chatId);
      s.loading = false;
      s.attempted = true;
      if (slot.current !== s) return;
      if (r.kind !== "ok") {
        setOffline(true);
        return;
      }
      setOffline(false);
      s.rev = r.revision;
      const local = s.latest;
      s.synced = r.body;
      const base = loadBaseRevision(email, s.chatId);
      // Text here that the server has not seen, over a server copy this browser was already in step with.
      const ahead = base !== null && base === r.revision;
      if (local === "" && r.body !== "" && loadCleared(email, s.chatId)) {
        // The box was emptied on purpose and the delete never reached the server. Finish it, unless the
        // server moved on since: then the other device's text is a conflict, not something to delete.
        if (!ahead) {
          s.paused = true;
          setConflict({ theirs: s.shareText === null ? r.body : withShare(r.body, s.shareText) });
        }
      } else if (local === "" || local === r.body || ahead || (r.body === "" && r.revision === 0)) {
        if (local === "" && r.body !== "") setDraftRef.current(r.body);
        s.latest = local === "" ? r.body : local;
        saveBaseRevision(email, s.chatId, r.revision);
        saveCleared(email, s.chatId, false);
      } else {
        // Both sides hold something the other has not seen. A server body of "" with a revision is a draft
        // that another device sent or deleted: this browser's older text must be chosen, not silently re-saved.
        s.paused = true;
        setConflict({ theirs: s.shareText === null ? r.body : withShare(r.body, s.shareText) });
      }
      s.loaded = true;
      void flush(s);
    },
    [flush, email],
  );

  // Switching chats loads that chat's draft; the chat being left is flushed first.
  useEffect(() => {
    // Seeded from this chat's own saved copy. The box still holds the previous chat's text until the composer
    // resets, and that text must never be read as this chat's.
    const first = slot.current === null; // nothing was on screen before: the box is this chat's own
    const own = first && draftRef.current !== "" ? draftRef.current : loadDraft(email, chatId);
    const s: Slot = { chatId, rev: 0, synced: "", latest: own, stale: !first && draftRef.current !== own ? draftRef.current : null, loaded: false, loading: false, attempted: false, paused: false, busy: false, box: own, held: false, shareText: null, shared: false };
    slot.current = s;
    setConflict(null);
    setOffline(false);
    setRefusal(null);
    void load(s);
    const onOnline = () => void (s.loaded ? flush(s) : load(s));
    window.addEventListener("online", onOnline);
    return () => {
      window.removeEventListener("online", onOnline);
      window.clearTimeout(timer.current);
      void flush(s); // the text typed just before leaving this chat still goes to this chat's draft
    };
  }, [chatId, flush, load, email]);

  // Debounced write of whatever is in the box (or, if the first read failed, another try at the read).
  useEffect(() => {
    const s = slot.current;
    if (!s || s.chatId !== chatId) return;
    if (s.stale !== null) {
      if (draft === s.stale) return; // still the previous chat's text: not this chat's draft
      s.stale = null;
    }
    s.latest = draft;
    s.box = draft;
    if (s.loaded && !s.paused && !s.held && draft === "" && s.synced !== "") saveCleared(email, s.chatId, true); // a deliberate empty box the server has not been told about
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => void (s.loaded ? flush(s) : load(s)), DEBOUNCE_MS);
  }, [draft, chatId, flush, load, email]);

  // Shared text lands in the new-chat draft only and is not sent. It is appended to the text in the box
  // and, when the server disagrees, to the server's candidate too, so either choice keeps it.
  useEffect(() => {
    if (share === null || chatId !== null) return;
    const s = slot.current;
    if (!s) return;
    const started = Date.now();
    const id = window.setInterval(() => {
      if (Date.now() - started > SHARE_WAIT_MS && !s.attempted) {
        // The first read never settled: do not wait for the server. The text goes into the box (this browser's
        // copy), and the normal conflict check runs if the read ever finishes.
        s.attempted = true;
      }
      if (slot.current !== s || !s.attempted) return; // wait for the first read to finish, whichever way it went
      window.clearInterval(id);
      if (s.shared) return;
      s.shared = true;
      s.shareText = share; // from here on every conflict candidate is built with it, wherever the conflict comes from
      setConflict((c) => (c ? { theirs: withShare(c.theirs, share) } : c));
      setDraftRef.current(withShare(s.latest, share));
      onSharedRef.current();
    }, 50);
    return () => window.clearInterval(id);
  }, [share, chatId]);

  return {
    conflict,
    offline,
    refusal,
    hold: () => {
      const s = slot.current;
      if (!s) return () => {};
      s.held = true;
      return (admitted) => {
        s.held = false;
        // Admitted: the box's current text is what the server should hold (empty = delete). Not admitted: leave the
        // server copy alone while the box is still empty, but anything typed since is saved like any other edit.
        s.latest = admitted || s.box !== "" ? s.box : s.synced;
        void flush(s);
      };
    },
    retry: () => {
      const s = slot.current;
      if (s) void (s.loaded ? flush(s) : load(s));
    },
    keepMine: () => {
      const s = slot.current;
      if (!s) return;
      s.paused = false;
      setConflict(null);
      void flush(s);
    },
    useTheirs: () => {
      const s = slot.current;
      if (!s || !conflict) return;
      s.latest = conflict.theirs; // s.synced stays the server's own text, so a share kept in `theirs` is written
      s.paused = false;
      saveBaseRevision(email, s.chatId, s.rev); // now in step with the server revision that was just read
      saveCleared(email, s.chatId, false);
      setDraftRef.current(conflict.theirs);
      setConflict(null);
    },
  };
}

export function DraftNotice({ sync }: { sync: DraftSyncView }) {
  if (sync.conflict) {
    return (
      <p className="draft-note" role="status">
        This draft was changed on another device. Your text is kept here and is not saved until you choose.{" "}
        <button type="button" className="quiet" onClick={sync.keepMine}>
          Keep this text
        </button>{" "}
        <button type="button" className="quiet" onClick={sync.useTheirs}>
          Use the other device&rsquo;s text
        </button>
      </p>
    );
  }
  if (sync.refusal) {
    return (
      <p className="draft-note" role="status">
        {sync.refusal}
      </p>
    );
  }
  if (sync.offline) {
    return (
      <p className="draft-note" role="status">
        This draft is saved in this browser only.{" "}
        <button type="button" className="quiet" onClick={sync.retry}>
          Retry
        </button>
      </p>
    );
  }
  return null;
}

// The share is read from the address bar once per page load and kept here until it is applied. A React
// initializer may run twice (StrictMode), so the second call must see the same text, not an empty bar.
let taken: string | null | undefined;

/** The text of an OS share, taken out of the address bar so a reload does not add it again. */
export function takeSharedText(): string | null {
  if (taken === undefined) {
    taken = sharedText(window.location.search);
    if (taken !== null) window.history.replaceState(null, "", "/");
  }
  return taken;
}

/** The share has been put in the draft: forget it. */
export function sharedTextApplied(): void {
  taken = null;
}

/** Tests only: start from a page that has not been read yet. */
export function resetSharedTextForTests(): void {
  taken = undefined;
}

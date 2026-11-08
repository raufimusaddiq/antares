// Autosave with compare-and-set. The author's text is kept in this browser (the same store and rules as chat drafts,
// so sign-out and the one-week limit clear it) until the server confirms it, and a stale save is never forced over
// a newer version: the author is asked (specs/001-blog-cms-openui-publish/research.md R7).

import { forgetChatDrafts, loadDraft, saveDraft } from "../drafts";
import { autosave as autosaveCall, type BlogResult } from "./blogApi";
import type { DocNode, Post } from "./types";

export type SaveState = "saved" | "saving" | "offline" | "conflict" | "error";

export interface Change {
  title?: string;
  summary?: string;
  doc?: DocNode;
}

type Save = (id: string, base: number, c: Change) => Promise<BlogResult<Post>>;

export interface AutosaverOptions {
  id: string;
  email: string;
  /** latest_version the editor was opened at. */
  version: number;
  onState: (s: SaveState, latest: Post | null) => void;
  /** A save the server accepted: the post as stored. */
  onSaved?: (p: Post) => void;
  save?: Save;
  delayMs?: number;
  retryMs?: number;
}

const key = (id: string) => `post:${id}`;

/** What this browser still holds for a post that the server has not confirmed. */
export function loadLocalCopy(email: string, id: string): Change | null {
  try {
    const raw = loadDraft(email, key(id));
    if (!raw) return null;
    const { base: _base, ...change } = JSON.parse(raw) as Change & { base?: number };
    return change;
  } catch {
    return null;
  }
}

/** The server version a kept local copy was typed against; null for a copy that does not say (it is then not trusted). */
export function localCopyBase(email: string, id: string): number | null {
  try {
    const raw = loadDraft(email, key(id));
    const base = raw ? (JSON.parse(raw) as { base?: unknown }).base : undefined;
    return typeof base === "number" ? base : null;
  } catch {
    return null;
  }
}

export function forgetLocalCopy(email: string, id: string): void {
  forgetChatDrafts(email, key(id));
}

export class Autosaver {
  state: SaveState = "saved";
  private version: number;
  private pending: Change | null = null;
  private busy = false;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private latest: Post | null = null;
  private dead = false;
  private inflight: Promise<void> | null = null;
  private pendingBase = 0; // the server version the unsent text was typed against
  private readonly save: Save;

  constructor(private readonly o: AutosaverOptions) {
    this.version = o.version;
    this.save = o.save ?? autosaveCall;
  }

  /** Records a change: the local copy now, the server soon. */
  change(c: Change): void {
    if (!this.pending) this.pendingBase = this.version;
    this.pending = { ...this.pending, ...c };
    saveDraft(this.o.email, key(this.o.id), JSON.stringify({ ...this.pending, base: this.pendingBase }));
    if (this.state === "conflict") return; // nothing is sent until the author chooses
    this.set("saving");
    this.schedule(this.o.delayMs ?? 800);
  }

  /** A kept copy that was typed against an older version than the server has now: held as it is, and the author chooses. */
  hold(c: Change, base: number, latest: Post): void {
    this.pending = c;
    this.pendingBase = base;
    this.latest = latest;
    this.set("conflict");
  }

  /** Sends now (leaving the editor, pressing Publish). True only if the server has everything typed so far. */
  async flush(): Promise<boolean> {
    clearTimeout(this.timer);
    while (this.inflight) await this.inflight;
    await this.run();
    return this.pending === null && this.state === "saved";
  }

  /** The author keeps their text over the newer server version. */
  keepMine(): void {
    if (this.state !== "conflict" || !this.latest) return;
    this.version = this.latest.latestVersion;
    this.pendingBase = this.version;
    this.latest = null;
    this.set("saving");
    void this.run();
  }

  /** The author takes the server's version; the local copy is dropped. */
  takeTheirs(): Post | null {
    const p = this.latest;
    this.pending = null;
    forgetLocalCopy(this.o.email, this.o.id);
    this.latest = null;
    if (p) this.version = p.latestVersion;
    this.set("saved");
    return p;
  }

  dispose(): void {
    this.dead = true;
    clearTimeout(this.timer);
  }

  private schedule(ms: number): void {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.run(), ms);
  }

  private set(s: SaveState): void {
    this.state = s;
    if (!this.dead) this.o.onState(s, this.latest);
  }

  private run(): Promise<void> {
    if (this.busy || !this.pending || this.state === "conflict") return Promise.resolve();
    const p: Promise<void> = this.send().finally(() => {
      if (this.inflight === p) this.inflight = null;
    });
    this.inflight = p;
    return p;
  }

  private async send(): Promise<void> {
    const sent = this.pending;
    if (!sent) return;
    this.busy = true;
    this.pending = null;
    const r = await this.save(this.o.id, this.version, sent);
    this.busy = false;
    if (this.dead) return;
    if (r.ok) {
      this.version = r.value.latestVersion;
      this.o.onSaved?.(r.value);
      if (this.pending as Change | null) {
        this.schedule(0); // typed while this save was in flight
        return;
      }
      forgetLocalCopy(this.o.email, this.o.id); // the server has it now
      this.set("saved");
      return;
    }
    const newer = this.pending as Change | null; // typed while this save was in flight
    this.pending = { ...sent, ...newer }; // nothing is lost: what was sent goes back under what is newer
    if (r.status === 409) {
      // The server has a newer version. Without it in the reply there is nothing to choose between, but sending the same
      // stale change again cannot succeed either: it waits, kept, and the author is told.
      this.latest = r.latest ?? null;
      this.set(r.latest ? "conflict" : "error");
      return;
    }
    this.set(r.code === "NETWORK" ? "offline" : "error");
    this.schedule(this.o.retryMs ?? 5000);
  }
}

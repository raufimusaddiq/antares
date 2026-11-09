import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { cancelCheck, decideBlock, describeBlogError, getCheck, getLimits, publish, startCheck } from "./blogApi";
import { checkBytes, imageCount, sizeWords, type Limits } from "./limits";
import { PreviewScreen } from "./PreviewScreen";
import { useDialog } from "./useDialog";
import type { Capabilities, Check, CheckBlock, Post } from "./types";

// Publish: choose, check, preview, publish. A failed, cancelled, timed-out or disconnected check is shown as itself,
// never as completed, and nothing is published until the author says so (FR-012, FR-013). The wording follows
// specs/001-blog-cms-openui-publish/contracts/check-envelope.md (Failure mapping).

const POLL_MS = 2000;
const UNSAVED = "Your latest changes are not saved yet, so the check would read an older version. Go back to editing and wait for “Saved”.";

export function failureWords(c: Check): string {
  switch (c.state) {
    case "timed_out":
      return "The check did not finish: the assistant timed out. It may or may not have started.";
    case "cancelled":
      return "Cancelled before the check finished.";
    case "out_of_date":
      return "The draft changed. Run the check again.";
    default:
      return c.reason === "disconnected" ? "Connection to the assistant was lost." : c.reason === "unusable" ? "The assistant's reply could not be used." : "The check did not finish: the assistant reported an error.";
  }
}

type Phase = { k: "choose" } | { k: "starting" } | { k: "checking" } | { k: "preview" } | { k: "published"; slug: string } | { k: "stopped" };

export interface PublishFlowProps {
  post: Post;
  caps: Capabilities | null;
  flush: () => Promise<boolean>;
  onPost: (p: Post) => void;
  onBackToEditing: () => void;
  onMyPosts: () => void;
}

export function PublishFlow({ post, caps, flush, onPost, onBackToEditing, onMyPosts }: PublishFlowProps) {
  const [phase, setPhase] = useState<Phase>(() => (!post.check ? { k: "choose" } : post.check.state === "running" ? { k: "checking" } : post.check.state === "ready" ? { k: "preview" } : { k: "stopped" }));
  const [check, setCheck] = useState<Check | null>(post.check);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [acks, setAcks] = useState<Record<string, boolean>>({});
  const [limits, setLimits] = useState<Limits | null>(null);
  const canCheck = caps?.check === true;
  const ready = post.title.trim() !== "";

  useEffect(() => {
    if (phase.k !== "choose") return;
    const ctl = new AbortController();
    void getLimits(ctl.signal).then((r) => {
      if (!ctl.signal.aborted && r.ok) setLimits(r.value); // not knowing the limits does not stop anyone publishing
    });
    return () => ctl.abort();
  }, [phase.k]);

  const apply = useCallback((c: Check) => {
    setCheck(c);
    setPhase(c.state === "running" ? { k: "checking" } : c.state === "ready" ? { k: "preview" } : { k: "stopped" });
  }, []);

  useEffect(() => {
    if (phase.k !== "checking") return;
    const ctl = new AbortController();
    let waiting = false; // one request at a time
    let halted = false; // a refusal from the server ends the polling; the author reloads to look again
    const t = setInterval(() => {
      if (waiting || halted) return;
      waiting = true;
      void getCheck(post.id, ctl.signal).then((r) => {
        waiting = false;
        if (ctl.signal.aborted) return;
        if (r.ok) apply(r.value);
        else if (r.status !== 0) {
          halted = true;
          setError(describeBlogError(r, "read the check") + " Reload the page to look again.");
        }
      });
    }, POLL_MS);
    return () => {
      ctl.abort();
      clearInterval(t);
    };
  }, [phase.k, post.id, apply]);

  const begin = async () => {
    setError("");
    setPhase({ k: "starting" });
    if (!(await flush())) { // the check reads the saved version
      setPhase({ k: "choose" });
      return setError(UNSAVED);
    }
    const r = await startCheck(post.id);
    if (r.ok) return apply(r.value);
    setPhase({ k: "choose" });
    setError(r.status === 409 ? "A check is already running for this post." : r.code === "CAPABILITY_UNAVAILABLE" ? "Checking is not available right now." : r.code === "TOO_LONG_TO_CHECK" ? "This post is longer than the assistant can read in one check. Publish your original, or shorten it." : describeBlogError(r, "start the check"));
  };

  const go = async (mode: "enriched" | "original") => {
    setBusy(true);
    setError("");
    if (mode === "original" && !(await flush())) {
      setBusy(false);
      return setError(UNSAVED);
    }
    const r = await publish(post.id, mode);
    setBusy(false);
    if (r.ok) {
      onPost(r.value);
      return setPhase({ k: "published", slug: r.value.slug });
    }
    setError(r.status === 409 ? "The address is taken by another published post, or the draft changed. Open Post settings." : r.status === 422 || r.status === 400 ? "Add a title and an address before publishing." : describeBlogError(r, "publish"));
  };

  const decide = async (b: CheckBlock, d: "accepted" | "rejected") => {
    if (!check) return;
    const r = await decideBlock(post.id, b.id, d, d === "accepted" && b.needsAck ? true : undefined);
    if (!r.ok) return setError(describeBlogError(r, "save that choice"));
    setCheck((c) => (c ? { ...c, blocks: c.blocks.map((x) => (x.id === b.id ? r.value : x)) } : c)); // decisions can overlap
  };

  // Accept every block (each one's tick was needed to get here), then publish with them. A refusal stops it where it is.
  const acceptAll = async () => {
    if (!check) return;
    setBusy(true);
    setError("");
    for (const b of check.blocks) {
      if (b.decision === "accepted") continue;
      const r = await decideBlock(post.id, b.id, "accepted", b.needsAck ? true : undefined);
      if (!r.ok) {
        setBusy(false);
        return setError(describeBlogError(r, "save that choice"));
      }
      setCheck((c) => (c ? { ...c, blocks: c.blocks.map((x) => (x.id === b.id ? r.value : x)) } : c));
    }
    setBusy(false);
    await go("enriched");
  };

  if (phase.k === "published")
    return (
      <Takeover step="published">
        <main className="blog-flow">
          <h1 tabIndex={-1}>Your post is live</h1>
          <p>Anyone can read it, without an account.</p>
          <div className="blog-actions">
            <a className="btn" href={`/b/${phase.slug}`}>
              View post
            </a>
            <button type="button" className="quiet" onClick={onMyPosts}>
              My posts
            </button>
          </div>
        </main>
      </Takeover>
    );

  if (phase.k === "checking" || phase.k === "starting")
    return (
      <Takeover step="checking" onBack={onBackToEditing}>
        <main className="blog-flow" aria-busy="true">
          <h1 tabIndex={-1}>Checking your writing</h1>
          <p role="status">Your assistant is reading the post, looking for data to add and finding sources. This can take a minute or two.</p>
          <p className="label">Nothing is published yet. Your original is unchanged.</p>
          <div className="blog-actions">
            <button
              type="button"
              className="quiet"
              disabled={phase.k === "starting"}
              onClick={() => void cancelCheck(post.id).then((r) => (r.ok ? apply(r.value) : setError(describeBlogError(r, "cancel the check"))))}
            >
              Cancel
            </button>
          </div>
          {error && <p className="blog-error" role="alert">{error}</p>}
        </main>
      </Takeover>
    );

  if (phase.k === "stopped" && check)
    return (
      <Takeover step="stopped" onBack={onBackToEditing}>
        <main className="blog-flow">
          <h1 tabIndex={-1}>{check.state === "out_of_date" ? "The draft changed" : "The check did not finish"}</h1>
          <p role="alert">{failureWords(check)}</p>
          <p className="label">Nothing was published.</p>
          <div className="blog-actions">
            {canCheck && (
              <button type="button" className="btn" onClick={() => void begin()}>
                Try again
              </button>
            )}
            <button type="button" className="quiet" disabled={busy || !ready} onClick={() => void go("original")}>
              Publish original
            </button>
            <button type="button" className="quiet" onClick={onBackToEditing}>
              Back to editing
            </button>
          </div>
          {error && <p className="blog-error" role="alert">{error}</p>}
        </main>
      </Takeover>
    );

  if (phase.k === "preview" && check)
    return (
      <Takeover step="preview" onBack={onBackToEditing}>
        <PreviewScreen
          post={post}
          check={check}
          acks={acks}
          onAck={(id, v) => setAcks((a) => ({ ...a, [id]: v }))}
          busy={busy}
          ready={ready}
          error={error}
          onDecide={(b, d) => void decide(b, d)}
          onAcceptAll={() => void acceptAll()}
          onPublishAccepted={() => void go("enriched")}
          onOriginal={() => void go("original")}
        />
      </Takeover>
    );

  return (
    <ChooseDialog
      post={post}
      ready={ready}
      canCheck={canCheck}
      busy={busy}
      limits={limits}
      error={error}
      onCheck={() => void begin()}
      onOriginal={() => void go("original")}
      onCancel={onBackToEditing}
    />
  );
}

/**
 * The first step, a dialog over the editor as the design has it: check and enrich, or publish what was written. Focus
 * moves in, Tab stays inside, Escape and Cancel go back to editing. The editor behind it is inert.
 */
function ChooseDialog({ post, ready, canCheck, busy, limits, error, onCheck, onOriginal, onCancel }: { post: Post; ready: boolean; canCheck: boolean; busy: boolean; limits: Limits | null; error: string; onCheck: () => void; onOriginal: () => void; onCancel: () => void }) {
  const ref = useDialog<HTMLDivElement>(onCancel);
  const tooLong = limits !== null && checkBytes(post.title, post.doc) > limits.checkBytes;
  const checkOk = canCheck && ready && !tooLong; // focus starts on the first choice that can be taken
  // The limits arrive after the dialog opens: a button that was enabled when focus moved in can become disabled (the post is too
  // long to check), which drops focus out of the dialog. Put it back on the first choice that can be taken.
  useEffect(() => {
    const el = ref.current;
    const now = document.activeElement as HTMLButtonElement | null;
    if (!el || (el.contains(now) && !now?.disabled)) return; // focus is on something that can be used
    (el.querySelector<HTMLElement>("[data-autofocus]:not(:disabled)") ?? el.querySelector<HTMLElement>("button:not(:disabled)"))?.focus();
  }, [ref, checkOk, ready]);
  return (
    <div className="blog-scrim">
      <div ref={ref} className="blog-dialog" role="dialog" aria-modal="true" aria-labelledby="publish-title" tabIndex={-1}>
        <h2 id="publish-title">Publish this post</h2>
        {!ready && <p className="blog-error" role="alert">Give the post a title before publishing.</p>}
        {canCheck ? (
          <section className="blog-option blog-option-check" aria-labelledby="opt-check">
            <h3 id="opt-check">Check and enrich</h3>
            <p>Your assistant reads the post, checks claims against the web, and proposes tables, charts and other blocks. You review everything before it goes live.</p>
            <button type="button" className="btn" data-autofocus={checkOk ? true : undefined} disabled={!ready || tooLong} onClick={onCheck}>
              Check and enrich
            </button>
          </section>
        ) : (
          <section className="blog-notice" role="status">
            <h3>Checking is not available right now</h3>
            <p>The assistant or its web search is not set up. You can still publish your post exactly as written.</p>
          </section>
        )}
        <section className="blog-option" aria-labelledby="opt-original">
          <h3 id="opt-original">Publish my original</h3>
          <p>Publish exactly what you wrote. No blocks are added.</p>
          <button type="button" className={canCheck ? "quiet" : "btn"} data-autofocus={!checkOk && ready ? true : undefined} disabled={busy || !ready} onClick={onOriginal}>
            Publish my original
          </button>
        </section>
        {limits && <LimitsNote limits={limits} post={post} canCheck={canCheck} />}
        {error && <p className="blog-error" role="alert">{error}</p>}
        <button type="button" className="quiet" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

/** What the author is held to, said before they publish and not after a failure. */
function LimitsNote({ limits, post, canCheck }: { limits: Limits; post: Post; canCheck: boolean }) {
  const bytes = checkBytes(post.title, post.doc);
  const images = imageCount(post.doc);
  const tooLong = bytes > limits.checkBytes;
  return (
    <section className="blog-limits" aria-label="Limits">
      <h3>Limits</h3>
      <ul>
        {canCheck && (
          <li className={tooLong ? "blog-error" : undefined} role={tooLong ? "alert" : undefined}>
            {tooLong
              ? `This post is longer than the assistant can read in one check (about ${sizeWords(bytes)}; the limit is ${sizeWords(limits.checkBytes)}). Publish your original, or shorten it.`
              : `The assistant reads up to ${sizeWords(limits.checkBytes)} of text in a check. This post is about ${sizeWords(bytes)}.`}
          </li>
        )}
        {canCheck && (
          <li>
            A check proposes up to {limits.blocksPerCheck} blocks, each naming up to {limits.sourcesPerBlock} sources, and you approve each one. You can start {limits.checksPer24h} checks in 24 hours.
          </li>
        )}
        <li>
          Images: {images} of {limits.imagesPerPost} in this post, up to {sizeWords(limits.imageBytes)} each, and up to {sizeWords(limits.imageBytesTotal)} across all your posts. A saved post can be up to {sizeWords(limits.docBytes)}.
        </li>
      </ul>
    </section>
  );
}

/**
 * A full-screen step of Publish. When one appears (the dialog has just gone, or the step changed) focus moves to its
 * heading, so the change is announced and the keyboard starts at the top of what is new.
 */
function Takeover({ step, onBack, children }: { step: string; onBack?: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector<HTMLElement>("h1")?.focus();
  }, [step]);
  return (
    <div ref={ref} className="blog-takeover">
      {onBack && (
        <header className="blog-takeover-bar">
          <button type="button" className="quiet" onClick={onBack}>
            ‹ Back to editing
          </button>
        </header>
      )}
      {children}
    </div>
  );
}

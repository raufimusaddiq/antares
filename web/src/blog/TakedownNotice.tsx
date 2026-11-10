import { useEffect, useState } from "react";
import { listNotices, markNoticeSeen } from "./blogApi";
import type { TakedownNotice } from "./types";

/** The one-time notice after an administrator takes down one of the account's posts. The server remembers that it was seen, so it
 * does not come back on another device or after a reload. Nothing is shown when there is none or the read fails. */
export function TakedownNotices({ onOpen }: { onOpen?: () => void }) {
  const [notices, setNotices] = useState<TakedownNotice[]>([]);
  useEffect(() => {
    const ctl = new AbortController();
    void listNotices(ctl.signal).then((r) => {
      if (!ctl.signal.aborted && r.ok) setNotices(r.value);
    });
    return () => ctl.abort();
  }, []);
  if (notices.length === 0) return null;
  const dismiss = () => {
    const seen = notices;
    setNotices([]);
    for (const n of seen) void markNoticeSeen(n.postId);
  };
  const one = notices.length === 1 ? notices[0] : null;
  return (
    <aside className="pwa-notice takedown-notice" aria-label="Post taken down">
      <p role="status">
        {one ? (
          <>
            An administrator took down your post “{one.title || "Untitled"}”.{one.reason ? ` Reason: ${one.reason}` : ""}
          </>
        ) : (
          <>An administrator took down {notices.length} of your posts: {notices.map((n) => `“${n.title || "Untitled"}”`).join(", ")}. Open My posts to see the reasons.</>
        )}
      </p>
      {onOpen && (
        <button type="button" className="quiet" onClick={() => { dismiss(); onOpen(); }}>
          See my posts
        </button>
      )}
      <button type="button" className="quiet" onClick={dismiss}>
        Got it
      </button>
    </aside>
  );
}

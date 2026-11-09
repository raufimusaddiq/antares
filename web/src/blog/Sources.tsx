import { formatDay } from "./dates";
import type { Source } from "./types";

/**
 * The sources of the selected block, as the design's "Sources for the selected block" drawer lists them. `verified` comes
 * from the server alone: an address the web search did not return is shown as unverified, never as checked (and today nothing
 * can be verified, so every one says unverified).
 */
export function SourceDrawer({ sources }: { sources: Source[] }) {
  return (
    <aside className="blog-drawer" aria-labelledby="drawer-h">
      <h3 id="drawer-h">Sources for the selected block</h3>
      {sources.length === 0 ? (
        <p className="label">This block names no sources.</p>
      ) : (
        <ul>
          {sources.map((s) => (
            <li key={s.url}>
              <a href={s.url} rel="noopener noreferrer nofollow" target="_blank">
                {s.title}
              </a>
              <span className="label">
                {new URL(s.url).hostname}
                {s.retrievedAt && ` · retrieved ${formatDay(s.retrievedAt)}`}
              </span>
              <span className={s.verified ? "chip chip-ok" : "chip chip-warn"}>{s.verified ? "verified" : "unverified"}</span>
            </li>
          ))}
        </ul>
      )}
    </aside>
  );
}

/**
 * What stands between the author and accepting a block whose sources were not checked (FR-017): how many, what that means, and
 * the tick. The block cannot be accepted until the tick is made; the server refuses it without. Once a block is accepted the tick
 * is a record, not a control (`locked`): reject it to start over.
 */
export function AckGate({ count, acked, locked = false, onAck }: { count: number; acked: boolean; locked?: boolean; onAck: (v: boolean) => void }) {
  return (
    <div className="blog-gate">
      <span className="chip chip-warn">
        {count} unverified {count === 1 ? "source" : "sources"}
      </span>
      <p>Antares has not checked these sources. To accept this block, confirm you have checked them yourself.</p>
      <label className="blog-ack">
        <input type="checkbox" checked={acked} disabled={locked} onChange={(e) => onAck(e.target.checked)} /> I have checked these sources myself
      </label>
    </div>
  );
}

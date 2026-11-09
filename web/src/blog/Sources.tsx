import { formatDay } from "./dates";
import type { Source } from "./types";

/**
 * The sources behind a proposed block, one row each. `verified` comes from the server alone: an address the web
 * search did not return is shown as unverified, never as checked. When any source is unverified the author must
 * tick the acknowledgement before the block can be accepted (FR-017).
 */
export function SourceList({ sources, needsAck, acked, onAck }: { sources: Source[]; needsAck: boolean; acked: boolean; onAck: (v: boolean) => void }) {
  return (
    <details className="blog-sources" open={needsAck}>
      <summary>
        Sources ({sources.length}){needsAck && " · not all verified"}
      </summary>
      <ul>
        {sources.map((s) => (
          <li key={s.url}>
            <a href={s.url} rel="noopener noreferrer nofollow" target="_blank">
              {s.title}
            </a>{" "}
            <span className="label">
              {new URL(s.url).hostname}
              {s.retrievedAt && ` · retrieved ${formatDay(s.retrievedAt)}`}
            </span>{" "}
            <span className={s.verified ? "chip chip-ok" : "chip chip-warn"}>{s.verified ? "verified" : "unverified"}</span>
          </li>
        ))}
      </ul>
      {needsAck && (
        <label className="blog-ack">
          <input type="checkbox" checked={acked} onChange={(e) => onAck(e.target.checked)} /> I have checked these sources myself
        </label>
      )}
    </details>
  );
}

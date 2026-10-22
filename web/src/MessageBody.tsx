import { lazy, Suspense, useMemo } from "react";
import { Markdown } from "./Markdown";
import { SourceFallback } from "./openui/Fallback";
import { splitSegments } from "./openui/segments";

// OpenUI is loaded only when a message holds a block (PRD PERF-001 item 4).
const RichBlock = lazy(() => import("./openui/Block"));

/**
 * An assistant message: Markdown, with OpenUI blocks rendered through the
 * allowlist where they are complete and valid. While a reply is still
 * streaming, an unfinished block is a quiet placeholder, never a half-built UI
 * (PRD §7.5); once the reply has ended, an unfinished or invalid block is shown
 * as its source. Markdown/plain text is always the fallback, never a blank.
 */
export function MessageBody({ text, streaming }: { text: string; streaming: boolean }) {
  const segments = useMemo(() => splitSegments(text), [text]);
  if (segments.length === 1 && segments[0].kind === "md") return <Markdown text={segments[0].text} />;
  return (
    <>
      {segments.map((s, i) => {
        if (s.kind === "md") return <Markdown key={i} text={s.text} />;
        if (!s.closed) {
          return streaming ? (
            <p key={i} className="label" role="status">
              Preparing rich content…
            </p>
          ) : (
            <SourceFallback key={i} source={s.source} why="incomplete" />
          );
        }
        return (
          <Suspense
            key={i}
            fallback={
              <p className="label" role="status">
                Loading rich content…
              </p>
            }
          >
            <RichBlock source={s.source} />
          </Suspense>
        );
      })}
    </>
  );
}

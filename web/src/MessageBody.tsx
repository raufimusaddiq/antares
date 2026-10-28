import { Component, lazy, Suspense, useMemo, type ReactNode } from "react";
import { Markdown } from "./Markdown";
import { SourceFallback } from "./openui/Fallback";
import { splitSegments } from "./openui/segments";

// OpenUI is loaded only when a message holds a block (PRD PERF-001 item 4).
const RichBlock = lazy(() => import("./openui/Block"));

// The chunk is fetched on demand, so the import can reject (offline, never loaded once). Suspense does
// not catch that, and an escaped error unmounts the whole app: fall back to the block's source instead.
class LoadBoundary extends Component<{ source: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? <SourceFallback source={this.props.source} why="could not load" /> : this.props.children;
  }
}

/**
 * An assistant message: Markdown, with OpenUI blocks rendered through OpenUI's chat library. While a
 * reply is still streaming, a block renders progressively; once the reply has ended, an unfinished or
 * invalid block is shown as its source. Markdown/plain text is always the fallback, never a blank.
 */
export function MessageBody({ text, streaming }: { text: string; streaming: boolean }) {
  const segments = useMemo(() => splitSegments(text), [text]);
  if (segments.length === 1 && segments[0].kind === "md") return <Markdown text={segments[0].text} />;
  return (
    <>
      {segments.map((s, i) => {
        if (s.kind === "md") return <Markdown key={i} text={s.text} />;
        // A fenced block is still open while the reply streams in; the renderer shows it progressively.
        if (!s.closed && !streaming) return <SourceFallback key={i} source={s.source} why="incomplete" />;
        return (
          <LoadBoundary key={i} source={s.source}>
            <Suspense
              fallback={
                <p className="label" role="status">
                  Loading rich content…
                </p>
              }
            >
              <RichBlock source={s.source} streaming={streaming} />
            </Suspense>
          </LoadBoundary>
        );
      })}
    </>
  );
}

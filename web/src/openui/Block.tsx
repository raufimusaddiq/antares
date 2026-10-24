import "./zodConfig"; // must stay first: see the file
import { Component, useMemo, type ReactNode } from "react";
import { Renderer } from "@openuidev/react-lang";
import { runAction } from "./actions";
import { library } from "./library";
import { SourceFallback } from "./Fallback";
import { check } from "./validate";

// The only module that imports the OpenUI runtime. It is loaded on demand
// (MessageBody.tsx), so a chat with no rich content never downloads it.

class Boundary extends Component<{ source: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? <SourceFallback source={this.props.source} why="render error" /> : this.props.children;
  }
}

// Model-declared actions arrive here. Components call runAction themselves with
// the composer sink; this channel has no sink, so whatever name the block carries
// is refused (an unknown name by the allowlist, "reply" for lack of a handler).
export const onAction = (e: { type?: unknown; params?: unknown }) => {
  runAction(e?.type, e?.params, {});
};

export default function RichBlock({ source }: { source: string }) {
  const verdict = useMemo(() => check(source), [source]);
  if (!verdict.ok) return <SourceFallback source={source} why={verdict.reason} />;
  return (
    <Boundary source={source}>
      <div className="ou-block">
        {/* Root + Content, not the all-in-one Renderer: that one adds a loading spinner
            which injects a <style> element, and our CSP (style-src 'self') refuses it.
            A validated block has no queries, so there is nothing to show a loader for. */}
        <Renderer.Root response={source} library={library} isStreaming={false} onAction={onAction}>
          <Renderer.Content />
        </Renderer.Root>
      </div>
    </Boundary>
  );
}

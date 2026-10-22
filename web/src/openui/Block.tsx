import "./zodConfig"; // must stay first: see the file
import { Component, useMemo, type ReactNode } from "react";
import { Renderer } from "@openuidev/react-lang";
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

const noAction = () => {
  // The library defines no action-capable component, so nothing can call this.
  // It exists so that, were one ever added, an action would do nothing until the
  // secured Go API route for it exists (PRD §7.4).
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
        <Renderer.Root response={source} library={library} isStreaming={false} onAction={noAction}>
          <Renderer.Content />
        </Renderer.Root>
      </div>
    </Boundary>
  );
}

import "./zodConfig"; // must stay first: see the file
import "@openuidev/react-ui/defaults.css";
import "@openuidev/react-ui/components.css";
import { Component, useContext, useMemo, useState, type ReactNode } from "react";
import { Renderer } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";
import { runAction, type Sinks } from "./actions";
import { ReplyContext } from "./reply";
import { SourceFallback } from "./Fallback";
import { check } from "./validate";

// The only module that imports the OpenUI runtime and its component library (native OpenUI: the
// openuiChatLibrary of @openuidev/react-ui). It is loaded on demand (MessageBody.tsx), so a chat with no
// rich content never downloads it. ThemeProvider is not used: it injects a <style> element, which the
// page's CSP (style-src 'self') refuses; the static defaults.css token sheet, themed by .openui-root in styles.css,
// does the same job.

class Boundary extends Component<{ source: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? <SourceFallback source={this.props.source} why="render error" /> : this.props.children;
  }
}

/** Opens an http(s) address the reader has confirmed, in a new tab, with no opener and no referrer. */
export function openConfirmed(url: string) {
  if (window.confirm(`Open this link in a new tab?\n\n${url}`)) window.open(url, "_blank", "noopener,noreferrer");
}

export default function RichBlock({ source, streaming = false }: { source: string; streaming?: boolean }) {
  const reply = useContext(ReplyContext);
  // A model can label a button "Submit order", but a follow-up only fills the composer: say so after the click (#173).
  const [added, setAdded] = useState(false);
  const toComposer = reply
    ? (text: string) => {
        reply(text);
        setAdded(true);
      }
    : null;
  const sinks: Sinks = { reply: toComposer, open: openConfirmed };
  // Every update is checked before it is rendered: a partial program against the gate's safety rules and limits,
  // the finished one against all of it. A failure is shown as source.
  const verdict = useMemo(() => check(source, streaming), [source, streaming]);
  if (!verdict.ok) return <SourceFallback source={source} why={verdict.reason} />;
  return (
    <Boundary source={source}>
      <div className="ou-block openui-root">
        {/* Root + Content, not the all-in-one Renderer: that one adds a loading spinner that injects a <style>
            element, which the CSP refuses. A program here has no queries, so nothing needs a loader. */}
        <Renderer.Root response={source} library={openuiChatLibrary} isStreaming={streaming} onAction={(e) => runAction(e?.type, e, sinks)}>
          <Renderer.Content />
        </Renderer.Root>
        {added && (
          <p className="label" role="status">
            Added to your message. Nothing was sent.
          </p>
        )}
      </div>
    </Boundary>
  );
}

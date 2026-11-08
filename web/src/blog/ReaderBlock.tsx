import "../openui/zodConfig"; // must stay first: see that file
import "@openuidev/react-ui/defaults.css";
import "@openuidev/react-ui/components.css";
import { Component, useMemo, type ReactNode } from "react";
import { Renderer } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";
import { check, repair } from "../openui/validate";

// A block on a published post, drawn for a reader. Readers can sort, expand and hover; they cannot act: there is no
// reply, no link out and no call to any API, so every action a block names is ignored. (T061 swaps in a library
// variant with the actions removed, which is the stronger form of the same rule.) Markup that does not pass the
// same checks as chat blocks, or fails to draw, shows its plain-text version instead (FR-021).

function Plain({ text }: { text: string }) {
  return (
    <div className="blog-block-plain">
      <p className="label">This block could not be shown. Plain version:</p>
      <p>{text}</p>
    </div>
  );
}

class Boundary extends Component<{ fallback: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? <Plain text={this.props.fallback} /> : this.props.children;
  }
}

export default function ReaderBlock({ markup, fallback }: { markup: string; fallback: string }) {
  const program = useMemo(() => repair(markup).program, [markup]);
  const verdict = useMemo(() => check(program, false), [program]);
  if (!verdict.ok) return <Plain text={fallback} />;
  return (
    <Boundary fallback={fallback}>
      <div className="ou-block openui-root">
        <Renderer.Root response={program} library={openuiChatLibrary} isStreaming={false} onAction={() => {}}>
          <Renderer.Content />
        </Renderer.Root>
      </div>
    </Boundary>
  );
}

import "../openui/zodConfig"; // must stay first: see that file
import "@openuidev/react-ui/defaults.css";
import "@openuidev/react-ui/components.css";
import { Component, useMemo, type ReactNode } from "react";
import { Renderer } from "@openuidev/react-lang";
import { readerLibrary } from "../openui/readerLibrary";
import { check, repair } from "../openui/validate";

// A block on a published post, drawn for a reader. Readers can sort, expand and hover; they cannot act: there is no
// reply, no link out and no call to any API. That holds three ways: the library is the read-only one (readerLibrary.ts:
// no forms, buttons, images or follow-ups, so a program using one is refused as unknown), the server refused such markup
// when the block was stored, and any action that somehow reached the renderer is ignored. Markup that does not pass the
// same gate as chat blocks, against the read-only library, or fails to draw, shows its plain-text version instead (FR-021).

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

/** The whole program is here, not a stream of it. */
const COMPLETE = false;

export default function ReaderBlock({ markup, fallback }: { markup: string; fallback: string }) {
  const program = useMemo(() => repair(markup, readerLibrary).program, [markup]);
  const verdict = useMemo(() => check(program, COMPLETE, readerLibrary), [program]);
  if (!verdict.ok) return <Plain text={fallback} />;
  return (
    <Boundary fallback={fallback}>
      <div className="ou-block openui-root">
        <Renderer.Root response={program} library={readerLibrary} isStreaming={false} onAction={() => {}}>
          <Renderer.Content />
        </Renderer.Root>
      </div>
    </Boundary>
  );
}

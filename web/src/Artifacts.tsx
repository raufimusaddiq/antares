import { useState } from "react";
import { artifactUrl, findArtifacts } from "./artifacts";

/**
 * Preview and download for the HTML/SVG blocks in one assistant turn. The preview
 * is an iframe whose `sandbox` has no tokens at all: no scripts, no same-origin,
 * no forms, popups, downloads or top navigation, so it gets an opaque origin that
 * cannot see our cookies, storage or DOM. The server also answers the frame URL
 * with a CSP `sandbox` and `default-src 'none'`. Never add `allow-scripts` together
 * with `allow-same-origin`, and never inline the markup into this document.
 */
export function Artifacts({ chatId, seq, text }: { chatId: string; seq: number; text: string }) {
  const [open, setOpen] = useState<number | null>(null);
  const items = findArtifacts(text);
  if (items.length === 0) return null;
  return (
    <ul className="artifacts" aria-label="Attachments">
      {items.map((a) => (
        <li key={a.index}>
          <span className="label">
            {a.kind.toUpperCase()} {a.index + 1}
          </span>{" "}
          <button type="button" className="quiet" aria-expanded={open === a.index} onClick={() => setOpen(open === a.index ? null : a.index)}>
            {open === a.index ? "Hide preview" : "Preview"}
          </button>{" "}
          <a className="btn" href={artifactUrl(chatId, seq, a.index)} download={`antares-turn${seq}-artifact${a.index + 1}.${a.kind}`}>
            Download
          </a>
          {open === a.index && (
            <iframe
              className="preview"
              title={`${a.kind.toUpperCase()} preview ${a.index + 1}`}
              sandbox=""
              referrerPolicy="no-referrer"
              loading="lazy"
              src={artifactUrl(chatId, seq, a.index, true)}
            />
          )}
        </li>
      ))}
    </ul>
  );
}

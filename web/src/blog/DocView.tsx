import { Fragment, lazy, Suspense, type ReactNode } from "react";
import { mediaUrl } from "./blogApi";
import type { DocNode, PublicBlock, Source } from "./types";

// Draws a stored document for readers and for the preview. There is no HTML path in this file: every node is a React
// element built from an allowlisted type, an unknown node draws nothing, and a link keeps only an http(s) or mailto
// address (specs/001-blog-cms-openui-publish FR-026). The server validates the same allowlist before storing.

const ReaderBlock = lazy(() => import("./ReaderBlock"));

/** An address a reader may follow, or "". */
export function safeHref(u: unknown): string {
  if (typeof u !== "string") return "";
  try {
    const p = new URL(u, "https://invalid.example");
    return p.protocol === "https:" || p.protocol === "http:" || p.protocol === "mailto:" ? u : "";
  } catch {
    return "";
  }
}

function text(n: DocNode, k: string): ReactNode {
  let out: ReactNode = n.text ?? "";
  for (const m of n.marks ?? []) {
    if (m.type === "bold") out = <strong>{out}</strong>;
    else if (m.type === "italic") out = <em>{out}</em>;
    else if (m.type === "code") out = <code>{out}</code>;
    else if (m.type === "link") {
      const href = safeHref(m.attrs?.href);
      if (href) out = (
        <a href={href} rel="noopener noreferrer nofollow" target="_blank">
          {out}
        </a>
      );
    }
  }
  return <Fragment key={k}>{out}</Fragment>;
}

const kids = (n: DocNode, k: string, imageUrl?: (src: string) => string): ReactNode[] => (n.content ?? []).map((c, i) => node(c, `${k}.${i}`, imageUrl));

function node(n: DocNode, k: string, imageUrl?: (src: string) => string): ReactNode {
  switch (n.type) {
    case "text":
      return text(n, k);
    case "paragraph":
      return <p key={k}>{kids(n, k, imageUrl)}</p>;
    case "heading": {
      const level = Number(n.attrs?.level);
      const Tag = level === 3 ? "h3" : level === 4 ? "h4" : "h2";
      return <Tag key={k}>{kids(n, k, imageUrl)}</Tag>;
    }
    case "bulletList":
      return <ul key={k}>{kids(n, k, imageUrl)}</ul>;
    case "orderedList":
      return <ol key={k}>{kids(n, k, imageUrl)}</ol>;
    case "listItem":
      return <li key={k}>{kids(n, k, imageUrl)}</li>;
    case "blockquote":
      return <blockquote key={k}>{kids(n, k, imageUrl)}</blockquote>;
    case "codeBlock":
      return (
        <pre key={k}>
          <code>{(n.content ?? []).map((c) => c.text ?? "").join("")}</code>
        </pre>
      );
    case "horizontalRule":
      return <hr key={k} />;
    case "hardBreak":
      return <br key={k} />;
    case "image": {
      const src = typeof n.attrs?.src === "string" ? n.attrs.src : "";
      const url = mediaUrl(imageUrl ? imageUrl(src) : src);
      return url ? <img key={k} src={url} alt={typeof n.attrs?.alt === "string" ? n.attrs.alt : ""} loading="lazy" /> : null;
    }
    default:
      return null; // an unknown node is not drawn, never guessed at
  }
}

export function SourcesLine({ sources }: { sources: Source[] }) {
  if (sources.length === 0) return null;
  return (
    <p className="blog-sources-line">
      {/* Every source is unverified today (nothing can verify one yet), so the second wording is for the day the sources can be verified. */}
      <span className="label">{sources.some((s) => !s.verified) ? "Sources named by the assistant, not independently checked:" : "Sources:"}</span>{" "}
      {sources.map((s, i) => (
        <span key={s.url}>
          {i > 0 && ", "}
          <a href={s.url} rel="noopener noreferrer nofollow" target="_blank">
            {new URL(s.url).hostname}
          </a>
        </span>
      ))}
      {sources[0].retrievedAt && <span className="label"> · retrieved {sources[0].retrievedAt}</span>}
    </p>
  );
}

/**
 * The document, with each AI-added block after the node it was placed behind. A block that cannot be drawn shows its
 * plain-text version (FR-021); the AI-added marker is always shown.
 */
export function DocView({ doc, blocks = [], imageUrl }: { doc: DocNode; blocks?: PublicBlock[]; imageUrl?: (src: string) => string }) {
  const top = doc.content ?? [];
  const after = (i: number) => blocks.filter((b) => b.anchor === i);
  const tail = blocks.filter((b) => b.anchor >= top.length);
  const draw = (b: PublicBlock, i: number) => (
    <section className="blog-block" key={`b${b.anchor}.${i}`} aria-label="Added by the assistant">
      <span className="chip chip-ai">AI-added</span>
      <Suspense fallback={<p className="label">Loading…</p>}>
        <ReaderBlock markup={b.markup} fallback={b.fallback} />
      </Suspense>
      <SourcesLine sources={b.sources} />
    </section>
  );
  return (
    <div className="blog-doc">
      {top.map((n, i) => (
        <Fragment key={i}>
          {node(n, String(i), imageUrl)}
          {after(i).map(draw)}
        </Fragment>
      ))}
      {tail.map(draw)}
    </div>
  );
}

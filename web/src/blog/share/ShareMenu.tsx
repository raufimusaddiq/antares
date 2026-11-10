import { useEffect, useRef, useState } from "react";
import { RowMenu, type RowAction } from "../RowMenu";
import { canonicalUrl, destinations } from "./shareLinks";

/**
 * Share on a published post (spec 002, the "Blog share" frames). It always opens a menu: on a device with a native sharing
 * sheet the first entry hands the title and the post's canonical address to it; elsewhere the menu lists the services. Copy
 * link and Story image are on every device. Every address that leaves is the canonical one, and nothing of any service is
 * loaded into the page.
 */
export function ShareMenu({ title, slug, onStory }: { title: string; slug: string; onStory?: () => void }) {
  const url = canonicalUrl(window.location.origin, slug);
  const [copied, setCopied] = useState(false);
  const [manual, setManual] = useState(false); // the Clipboard API refused: the address is shown to copy by hand
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (manual) field.current?.select();
  }, [manual]);

  const copy = () => {
    const done = () => {
      setManual(false);
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 2500);
    };
    const nav = navigator.clipboard;
    if (!nav?.writeText) return setManual(true);
    nav.writeText(url).then(done, () => setManual(true));
  };

  const native = typeof navigator.share === "function";
  const actions: RowAction[] = [];
  if (native) {
    actions.push({
      label: "Share with your apps",
      run: () => void navigator.share({ title, url }).catch(() => {}), // a cancelled sheet is not an error
    });
  }
  actions.push({ id: "copy", label: copied ? "Link copied" : "Copy link", keepOpen: true, done: copied, run: copy });
  if (!native) for (const d of destinations(title, url)) actions.push({ label: d.label, href: d.href, external: true });
  if (onStory) actions.push({ label: "Story image", divider: true, run: onStory });

  return (
    <div className="blog-share">
      <RowMenu label="Share this post" actions={actions} trigger="Share" triggerClass="quiet" triggerIcon="share" />
      <span className="label blog-share-note" role="status">
        {copied ? "Link copied" : ""}
      </span>
      {manual && (
        <div className="blog-share-manual">
          <label>
            <span className="label">Copy this address</span>
            <input ref={field} readOnly value={url} onFocus={(e) => e.currentTarget.select()} />
          </label>
        </div>
      )}
    </div>
  );
}

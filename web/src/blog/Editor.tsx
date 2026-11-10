import { useEffect, useRef, useState } from "react";
import { EditorContent, useEditor } from "@tiptap/react";
import { pressed, useFormatState, type Tri } from "./formatState";
import StarterKit from "@tiptap/starter-kit";
import Image from "@tiptap/extension-image";
import { Autosaver, loadLocalCopy, localCopyBase, type SaveState } from "./autosave";
import { describeBlogError, uploadImage } from "./blogApi";
import { safeHref } from "./DocView";
import { formatDay } from "./dates";
import type { DocNode, Post } from "./types";

// The writing surface, from the Editor frames of the design file. It is lazy-loaded (BlogApp) so readers and chat
// users never download it. The document is the editor's JSON; no HTML is stored or sent.

const SAVE_WORDS: Record<SaveState, string> = {
  saved: "Saved",
  saving: "Saving…",
  offline: "Offline: your text is kept on this device and will save when the connection returns.",
  conflict: "This post changed somewhere else.",
  error: "Could not save. Retrying…",
};

export interface EditorProps {
  post: Post;
  email: string;
  onPost: (p: Post) => void;
  onSettings: () => void;
  onPublish: (flush: () => Promise<boolean>) => void;
  onBack: () => void;
}

/** True for a document with nothing in it: no nodes, or only paragraphs with no content. */
function docIsEmpty(doc: DocNode | undefined): boolean {
  // The same meaning as the editor's own isEmpty: text blocks (paragraph, heading, …) holding nothing, or only empty text.
  const empty = (n: DocNode): boolean => {
    if (n.type === "text") return !n.text;
    if (n.type === "image" || n.type === "horizontalRule" || n.type === "hardBreak") return false;
    return (n.content ?? []).every(empty);
  };
  return (doc?.content ?? []).every(empty);
}

const BODY_HINT = "Tell your story…";

export default function Editor({ post, email, onPost, onSettings, onPublish, onBack }: EditorProps) {
  const [save, setSave] = useState<SaveState>("saved");
  const [latest, setLatest] = useState<Post | null>(null);
  const [title, setTitle] = useState(() => loadLocalCopy(email, post.id)?.title ?? post.title);
  const [note, setNote] = useState("");
  // Seeded from the text the editor opens with (the saved one or a kept local copy), so an empty post shows its placeholder on the first paint.
  const [empty, setEmpty] = useState(() => docIsEmpty(loadLocalCopy(email, post.id)?.doc ?? post.doc));
  const saver = useRef<Autosaver | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const editor = useEditor({
    injectCSS: false, // see the rules at the end of styles.css: the CSP refuses an injected <style>
    extensions: [StarterKit.configure({ heading: { levels: [2, 3, 4] }, link: { openOnClick: false, autolink: false, protocols: ["https", "http", "mailto"] } }), Image],
    content: (loadLocalCopy(email, post.id)?.doc ?? post.doc) as object,
    // The hint element is always there and only its text changes, so the attribute never has to be set or removed (the editor
    // re-applies these options on every render).
    editorProps: { attributes: { "aria-label": "Post body", "aria-describedby": "blog-body-hint", class: "blog-prose" } },
    onUpdate: ({ editor: e }) => {
      setEmpty(e.isEmpty);
      saver.current?.change({ doc: e.getJSON() as DocNode });
    },
  });

  useEffect(() => {
    if (editor) setEmpty(editor.isEmpty);
  }, [editor]);


  useEffect(() => {
    const s = new Autosaver({ id: post.id, email, version: post.latestVersion, onState: (st, l) => (setSave(st), setLatest(l)), onSaved: onPost });
    saver.current = s;
    // A kept local copy typed against the server's current version is sent at once. One typed against an older version
    // is never sent over what the server has since: the author chooses.
    const local = loadLocalCopy(email, post.id);
    if (local) {
      const base = localCopyBase(email, post.id);
      if (base === post.latestVersion) s.change(local);
      else s.hold(local, base ?? 0, post);
    }
    const hide = () => document.visibilityState === "hidden" && void s.flush();
    document.addEventListener("visibilitychange", hide);
    const online = () => void s.flush();
    window.addEventListener("online", online);
    return () => {
      document.removeEventListener("visibilitychange", hide);
      window.removeEventListener("online", online);
      void s.flush().finally(() => s.dispose());
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one saver per opened post
  }, [post.id]);

  const takeTheirs = () => {
    const p = saver.current?.takeTheirs();
    if (p) {
      onPost(p);
      setTitle(p.title);
      editor?.commands.setContent(p.doc as object);
    }
  };

  const addImage = async (file: File) => {
    setNote("Uploading image…");
    const r = await uploadImage(post.id, file);
    if (!r.ok) return setNote(r.status === 415 ? "That file type is not supported. Use PNG, JPEG, WebP or GIF." : describeBlogError(r, "upload the image"));
    setNote("");
    editor?.chain().focus().setImage({ src: r.value.url, alt: "" }).run();
  };

  const link = () => {
    const prev = editor?.getAttributes("link").href as string | undefined;
    const url = window.prompt("Link address (https://…)", prev ?? "https://");
    if (url === null) return;
    if (url === "") editor?.chain().focus().unsetLink().run();
    // safeHref alone accepts "example.com" (it resolves against a base); a stored link must carry its own scheme.
    else if (/^(https?:\/\/|mailto:)/i.test(url.trim()) && safeHref(url.trim())) editor?.chain().focus().setLink({ href: url.trim() }).run();
    else setNote("A link must start with https://, http:// or mailto:.");
  };

  const fmt = useFormatState(editor);
  const b = (label: string, on: () => void, state?: Tri) => (
    <button type="button" className="quiet" aria-pressed={state === undefined ? undefined : pressed(state)} aria-label={label} onClick={on}>
      {label}
    </button>
  );
  const chain = () => editor!.chain().focus();

  return (
    <main className="blog-editor">
      <header className="blog-bar">
        <button type="button" className="quiet" onClick={onBack}>
          My posts
        </button>
        <span className={`blog-save blog-save-${save}`} role="status">
          {SAVE_WORDS[save]}
        </span>
        <button type="button" className="quiet" onClick={onSettings}>
          Post settings
        </button>
        <button type="button" className="btn" data-publish disabled={save === "conflict" || !!post.takedown} aria-describedby={post.takedown ? "blog-takedown" : undefined} onClick={() => onPublish(() => saver.current?.flush() ?? Promise.resolve(false))}>
          Publish
        </button>
      </header>
      {post.takedown && (
        <p id="blog-takedown" className="blog-takedown" role="status">
          An administrator took this post down on {formatDay(post.takedown.at, false)}{post.takedown.reason ? `. Reason: ${post.takedown.reason}` : "."} You can keep editing, but it cannot be published again until it is restored.
        </p>
      )}
      {save === "conflict" && latest && (
        <div className="blog-conflict" role="alert">
          <p>This post was changed in another tab or device. Your latest text is kept here.</p>
          <button type="button" className="btn" onClick={() => saver.current?.keepMine()}>
            Keep my text
          </button>
          <button type="button" className="quiet" onClick={takeTheirs}>
            Use the saved version
          </button>
        </div>
      )}
      <input
        className="blog-title"
        aria-label="Title"
        placeholder="Title"
        value={title}
        maxLength={200}
        onChange={(e) => {
          setTitle(e.target.value);
          saver.current?.change({ title: e.target.value });
        }}
      />
      {editor && (
        <div className="blog-toolbar" role="toolbar" aria-label="Formatting">
          {b("Bold", () => chain().toggleBold().run(), fmt.bold)}
          {b("Italic", () => chain().toggleItalic().run(), fmt.italic)}
          {b("Heading", () => chain().toggleHeading({ level: 2 }).run(), fmt.heading2)}
          {b("Subheading", () => chain().toggleHeading({ level: 3 }).run(), fmt.heading3)}
          {b("Bullets", () => chain().toggleBulletList().run(), fmt.bullets)}
          {b("Numbers", () => chain().toggleOrderedList().run(), fmt.numbers)}
          {b("Quote", () => chain().toggleBlockquote().run(), fmt.quote)}
          {b("Code", () => chain().toggleCodeBlock().run(), fmt.code)}
          {b("Link", link, fmt.link)}
          {b("Image", () => fileInput.current?.click())}
          {b("Divider", () => chain().setHorizontalRule().run())}
          <input
            ref={fileInput}
            type="file"
            hidden
            accept="image/png,image/jpeg,image/webp,image/gif"
            onChange={(e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (f) void addImage(f);
            }}
          />
        </div>
      )}
      {note && (
        <p className="label" role="status">
          {note}
        </p>
      )}
      {/* An empty body is a blank box: say where to type. A placeholder drawn by CSS, so no editor extension is needed. */}
      <div className="blog-body" data-empty={empty ? "true" : undefined}>
        <span id="blog-body-hint" className="blog-sr">
          {empty ? BODY_HINT : ""}
        </span>
        <EditorContent editor={editor} />
      </div>
    </main>
  );
}

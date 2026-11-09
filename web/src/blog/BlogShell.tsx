import type { ReactNode } from "react";
import { AntaresMark } from "../brand/AntaresMark";

/**
 * My posts as the design has it: a rail beside the page (the brand, New post, Chats, Blog posts, the account line). On a phone the
 * rail becomes a strip above it. Chats is the way back to the conversations, where the account menu (Settings, Sign out) lives.
 */
export function BlogShell({ email, onNewPost, onChats, children }: { email: string; onNewPost: () => void; onChats: () => void; children: ReactNode }) {
  return (
    <div className="blog-shell">
      <nav className="blog-rail" aria-label="Antares">
        <strong className="brand">
          <AntaresMark size={20} />
          Antares
        </strong>
        <button type="button" className="btn rail-new" onClick={onNewPost}>
          New post
        </button>
        <button type="button" className="quiet rail-item" onClick={onChats}>
          Chats
        </button>
        <span className="rail-item" aria-current="page">
          Blog posts
        </span>
        <p className="who-am-i blog-rail-foot">{email}</p>
      </nav>
      {children}
    </div>
  );
}

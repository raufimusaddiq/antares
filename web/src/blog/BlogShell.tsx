import { useEffect, useState, type ReactNode } from "react";
import { AntaresMark } from "../brand/AntaresMark";
import { SettingsDialog, type SettingsTab } from "../SettingsDialog";

/**
 * My posts in the chat's shell: the rail beside the page, the brand and the one primary action, the ways elsewhere, and
 * the account line whose Menu holds Settings and Sign out. On a phone the rail is the strip above the page. The editor
 * collapses this rail: its own bar keeps "My posts" as the way back.
 */
export function BlogShell({
  email,
  onNewPost,
  onChats,
  onReadBlog,
  onSignOut,
  children,
}: {
  email: string;
  onNewPost: () => void;
  onChats: () => void;
  onReadBlog: () => void;
  onSignOut: (everywhere?: unknown) => void;
  children: ReactNode;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [settings, setSettings] = useState<SettingsTab | null>(null);
  useEffect(() => {
    if (!menuOpen) return;
    const away = (e: PointerEvent) => {
      if (!(e.target as Element | null)?.closest?.(".bar")) setMenuOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [menuOpen]);
  return (
    <>
      <div className="chat-app blog-app">
        <header
          className="bar"
          onKeyDown={(e) => {
            if (e.key === "Escape" && menuOpen) setMenuOpen(false);
          }}
        >
          <strong className="brand">
            <AntaresMark size={20} />
            Antares
          </strong>
          <div className="account">
            <span className="who-am-i">{email}</span>
            <button
              type="button"
              className="quiet bar-toggle"
              data-icon="chevron"
              aria-expanded={menuOpen}
              aria-controls="blog-bar-actions"
              onClick={() => setMenuOpen((o) => !o)}
            >
              Menu
            </button>
            <div id="blog-bar-actions" className={menuOpen ? "bar-actions open" : "bar-actions"}>
              <button
                type="button"
                className="quiet"
                data-icon="settings"
                onClick={() => {
                  setMenuOpen(false);
                  setSettings("general");
                }}
              >
                Settings
              </button>
              <button type="button" className="quiet" data-icon="logout" onClick={() => onSignOut()}>
                Sign out
              </button>
            </div>
          </div>
        </header>
        <nav className="side" aria-label="Antares">
          <button type="button" data-icon="plus" data-new-post onClick={onNewPost}>
            New post
          </button>
          <button type="button" className="quiet rail-blog" data-icon="arrow-left" onClick={onChats}>
            Chats
          </button>
          <span className="rail-item" aria-current="page">
            Blog posts
          </span>
          <button type="button" className="quiet rail-blog" data-icon="book" onClick={onReadBlog}>
            Read the blog
          </button>
        </nav>
        {children}
      </div>
      {settings && (
        <SettingsDialog
          tab={settings}
          jobs={false}
          onClose={() => {
            setSettings(null);
            document.querySelector<HTMLElement>(".chat-app .bar-toggle")?.focus();
          }}
          onSignOutEverywhere={() => onSignOut(true)}
        />
      )}
    </>
  );
}

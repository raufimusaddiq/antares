import { useCallback, useEffect, useState } from "react";
import { loadSession, signOut, type Session } from "./api";
import { Chat } from "./Chat";
import { Screen, documentTitle, type View } from "./Screen";

const SCREEN_PATH = {
  PENDING: "/pending",
  REJECTED: "/rejected",
  SUSPENDED: "/suspended",
  SETTING_UP: "/setup",
  ERROR: "/error",
  ACTIVE: "/",
} as const;

// The server redirects a failed Google sign-in to /error?code=login_failed.
const loginFailed = () => window.location.pathname === "/error" && new URLSearchParams(window.location.search).get("code") === "login_failed";

function viewOf(s: Session): View | null {
  switch (s.kind) {
    case "loading":
      return null;
    case "anonymous":
      return s.loginFailed ? { screen: "login-failed" } : { screen: "login" };
    case "account":
      return { screen: "account", account: s.account };
    case "unreachable":
      return { screen: "unreachable" };
  }
}

export function App() {
  const [session, setSession] = useState<Session>({ kind: "loading" });
  const [failedSignOut, setFailedSignOut] = useState(false);
  const [failed] = useState(loginFailed);

  const refresh = useCallback(() => {
    const ctl = new AbortController();
    void loadSession(failed, ctl.signal).then((s) => !ctl.signal.aborted && setSession(s));
    return () => ctl.abort();
  }, [failed]);

  useEffect(() => refresh(), [refresh]);

  const view = viewOf(session);

  const onSignOut = () => {
    void signOut().then((ok) => {
      setFailedSignOut(!ok);
      if (ok) setSession({ kind: "anonymous", loginFailed: false });
    });
  };

  // Keep the address bar on the screen that is showing, so a reload lands on it.
  useEffect(() => {
    if (session.kind === "account") window.history.replaceState(null, "", SCREEN_PATH[session.account.state]);
    else if (session.kind === "anonymous" && !session.loginFailed) window.history.replaceState(null, "", "/login");
  }, [session]);

  useEffect(() => {
    if (view) document.title = documentTitle(view);
  }, [view]);

  if (!view) return <main aria-busy="true"><p>Loading…</p></main>;
  if (session.kind === "account" && session.account.state === "ACTIVE") {
    return <Chat email={session.account.email} onSignOut={onSignOut} />;
  }
  return (
    <Screen
      view={view}
      signOutFailed={failedSignOut}
      onRefresh={() => {
        setSession({ kind: "loading" });
        refresh();
      }}
      onSignOut={onSignOut}
    />
  );
}

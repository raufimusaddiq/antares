import { useCallback, useEffect, useState } from "react";
import { loadSession, signOut, type Session } from "./api";
import { AdminScreens, AdminShell, Forbidden } from "./Admin";
import { Chat } from "./Chat";
import { isAdminPath, parseAdminPath } from "./route";
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
  const [path, setPath] = useState(() => window.location.pathname);

  useEffect(() => {
    const onPop = () => setPath(window.location.pathname);
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const navigate = useCallback((to: string) => {
    window.history.pushState(null, "", to);
    setPath(to);
  }, []);

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
    // An active account may be on an admin screen: keep that address instead of resetting it to "/".
    if (session.kind === "account") {
      if (session.account.state === "ACTIVE" && isAdminPath(window.location.pathname)) return;
      window.history.replaceState(null, "", SCREEN_PATH[session.account.state]);
      setPath(SCREEN_PATH[session.account.state]);
    }
    else if (session.kind === "anonymous" && !session.loginFailed) window.history.replaceState(null, "", "/login");
  }, [session]);

  useEffect(() => {
    if (view) document.title = documentTitle(view);
  }, [view]);

  if (!view) return <main className="screen" aria-busy="true"><p>Loading…</p></main>;
  if (session.kind === "account" && session.account.state === "ACTIVE") {
    if (isAdminPath(path)) {
      const route = parseAdminPath(path);
      return (
        <AdminShell email={session.account.email} route={route} onNavigate={navigate} onSignOut={onSignOut}>
          {/* canAdmin only spares a non-admin a round of failing calls; the server guards each one. */}
          {session.account.canAdmin ? <AdminScreens route={route} onNavigate={navigate} /> : <Forbidden />}
        </AdminShell>
      );
    }
    return <Chat email={session.account.email} canAdmin={session.account.canAdmin} onAdmin={() => navigate("/admin/users")} onSignOut={onSignOut} />;
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

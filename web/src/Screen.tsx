import type { Account, AccountState } from "./api";
import { LOGIN_URL } from "./api";

/** What a screen shows. Pure data in, markup out: no fetching here. */
export type View =
  | { screen: "login" }
  | { screen: "login-failed" }
  | { screen: "unreachable" }
  | { screen: "account"; account: Account };

interface Props {
  view: View;
  onRefresh: () => void;
  onSignOut: () => void;
  /** Why the last sign-out did not fully work; empty when it did. */
  signOutMessage?: string;
}

interface Copy {
  title: string;
  heading: string;
  message: string;
  next?: string;
  refresh?: boolean;
}

const COPY: Record<AccountState, Copy> = {
  PENDING: {
    title: "Waiting for approval",
    heading: "Waiting for administrator approval",
    message: "Your account has been created, but nobody has approved it yet, so chat is not available.",
    next: "You do not need to do anything else. Ask the person who runs this service to approve you, then refresh this page.",
    refresh: true,
  },
  REJECTED: {
    title: "Access not approved",
    heading: "Your access request was not approved",
    message: "An administrator reviewed your account and did not approve it. Chat is not available.",
    next: "If you think this is a mistake, contact the person who runs this service. You can sign out and use a different Google account.",
  },
  SUSPENDED: {
    title: "Access disabled",
    heading: "Your access is disabled",
    message: "An administrator has suspended this account. Chat is not available and no conversations are shown.",
    next: "Contact the person who runs this service to ask for access to be restored.",
  },
  SETTING_UP: {
    title: "Setting up",
    heading: "Your account is approved — your workspace is being prepared",
    message: "Chat will open as soon as your private workspace is ready. Until then it is not available.",
    next: "This usually does not need anything from you. Refresh in a few minutes; if it stays like this, contact the person who runs this service.",
    refresh: true,
  },
  ERROR: {
    title: "Something went wrong",
    heading: "Something went wrong",
    message: "Your account is approved, but its workspace is not working, so chat is not available.",
    next: "This needs an administrator. Contact the person who runs this service.",
  },
  // The app shows the chat itself for an active account (Chat.tsx); this is only
  // the fallback if a Screen is ever asked to render one.
  ACTIVE: {
    title: "Signed in",
    heading: "You are signed in",
    message: "Your workspace is ready.",
    refresh: true,
  },
};

function copyFor(view: View): Copy {
  switch (view.screen) {
    case "login":
      return {
        title: "Sign in",
        heading: "Sign in to Antares",
        message: "Access is by invitation: after you sign in, an administrator has to approve your account before you can chat.",
      };
    case "login-failed":
      return {
        title: "Something went wrong",
        heading: "Sign-in did not complete",
        message: "Google sign-in could not be verified, so you have not been signed in. Nothing was changed on your account.",
        next: "Go back and try signing in again.",
      };
    case "unreachable":
      return {
        title: "Something went wrong",
        heading: "Could not reach Antares",
        message: "The service did not answer, so your account status is unknown. Nothing is shown as available.",
        next: "Check your connection and try again. If it keeps happening, contact the person who runs this service.",
        refresh: true,
      };
    case "account":
      return COPY[view.account.state];
  }
}

export function Screen({ view, onRefresh, onSignOut, signOutMessage = "" }: Props) {
  const c = copyFor(view);
  const account = view.screen === "account" ? view.account : null;
  const showLogin = view.screen === "login" || view.screen === "login-failed";
  return (
    <main className="screen">
      <h1>{c.heading}</h1>
      <p>{c.message}</p>
      {account && (
        <dl>
          <dt>Signed in as</dt>
          <dd>
            {account.displayName ? `${account.displayName} · ` : ""}
            {account.email}
          </dd>
          {account.requestedAt && (
            <>
              <dt>Requested</dt>
              <dd>{account.requestedAt}</dd>
            </>
          )}
        </dl>
      )}
      {c.next && <p>{c.next}</p>}
      {showLogin && (
        <>
          <a className="btn" href={LOGIN_URL}>
            Sign in with Google
          </a>
          {view.screen === "login" && (
            <p>
              <small>
                Antares uses Google only to confirm who you are. It receives your name, email address and picture. There is no
                password.
              </small>
            </p>
          )}
        </>
      )}
      {c.refresh && (
        <button type="button" className="quiet" data-icon="regenerate" onClick={onRefresh}>
          Refresh
        </button>
      )}
      {account && (
        <button type="button" className="quiet" data-icon="logout" onClick={onSignOut}>
          Sign out
        </button>
      )}
      {signOutMessage && <p role="alert">{signOutMessage}</p>}
    </main>
  );
}

export function documentTitle(view: View): string {
  return `${copyFor(view).title} · Antares`;
}

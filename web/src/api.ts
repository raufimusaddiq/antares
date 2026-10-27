import { purgeDrafts, purgeStaleDrafts } from "./drafts";
import { purgeUserState } from "./pwa";

// The browser's whole view of the backend for this slice: who am I, and sign out.
// The server decides the state; nothing here computes eligibility.

export type AccountState = "PENDING" | "REJECTED" | "SUSPENDED" | "SETTING_UP" | "ERROR" | "ACTIVE";

const STATES: readonly string[] = ["PENDING", "REJECTED", "SUSPENDED", "SETTING_UP", "ERROR", "ACTIVE"];

/** The fields of GET /api/me that the screens use. */
export interface Account {
  email: string;
  displayName: string;
  requestedAt: string;
  state: AccountState;
  /** The server's verdict (GET /api/me canAdmin). It only decides whether to show a link; every admin call is authorised again. */
  canAdmin: boolean;
}

export type Session =
  | { kind: "loading" }
  | { kind: "anonymous"; loginFailed: boolean }
  | { kind: "account"; account: Account }
  | { kind: "unreachable" };

export async function loadSession(loginFailed: boolean, signal?: AbortSignal): Promise<Session> {
  try {
    const res = await fetch("/api/me", { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
    if (res.status === 401) {
      purgeUserState();
      purgeDrafts();
      return { kind: "anonymous", loginFailed };
    }
    if (!res.ok) return { kind: "unreachable" };
    const body: unknown = await res.json();
    const account = parseAccount(body);
    if (account?.state === "SUSPENDED") {
      purgeUserState();
      purgeDrafts();
    } else if (account) {
      purgeStaleDrafts(account.email); // another account's drafts, and this one's older than a week, leave the device
    }
    return account ? { kind: "account", account } : { kind: "unreachable" };
  } catch {
    return { kind: "unreachable" };
  }
}

/** Anything unexpected is "unreachable", never a guessed state: failure stays visible. */
export function parseAccount(body: unknown): Account | null {
  if (typeof body !== "object" || body === null) return null;
  const b = body as Record<string, unknown>;
  if (typeof b.state !== "string" || !STATES.includes(b.state)) return null;
  return {
    email: typeof b.email === "string" ? b.email : "",
    displayName: typeof b.displayName === "string" ? b.displayName : "",
    requestedAt: typeof b.requestedAt === "string" ? b.requestedAt : "",
    state: b.state as AccountState,
    canAdmin: b.canAdmin === true,
  };
}

/**
 * "ok": the server no longer considers this browser signed in (and, for everywhere, ended every session of the account).
 * "expired": this browser's session had already ended, so sign out everywhere could not run: other devices may still be
 * signed in, and the reader must sign in again to end them. "failed": nothing is known to have changed; try again.
 */
export type SignOutResult = "ok" | "expired" | "failed";

export async function signOut(everywhere = false): Promise<SignOutResult> {
  try {
    const res = await fetch(everywhere ? "/api/auth/logout-all" : "/api/auth/logout", { method: "POST", credentials: "same-origin" });
    if (res.status !== 204 && res.status !== 401) return "failed";
    purgeUserState(); // either way this browser is signed out now
    purgeDrafts();
    // A plain sign-out that finds no session has done its job; sign out everywhere has not (#150 review).
    return res.status === 401 && everywhere ? "expired" : "ok";
  } catch {
    return "failed";
  }
}

export const LOGIN_URL = "/api/auth/google/start";

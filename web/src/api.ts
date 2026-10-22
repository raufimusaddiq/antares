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
}

export type Session =
  | { kind: "loading" }
  | { kind: "anonymous"; loginFailed: boolean }
  | { kind: "account"; account: Account }
  | { kind: "unreachable" };

export async function loadSession(loginFailed: boolean, signal?: AbortSignal): Promise<Session> {
  try {
    const res = await fetch("/api/me", { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
    if (res.status === 401) return { kind: "anonymous", loginFailed };
    if (!res.ok) return { kind: "unreachable" };
    const body: unknown = await res.json();
    const account = parseAccount(body);
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
  };
}

/** True once the server no longer considers the browser signed in. */
export async function signOut(): Promise<boolean> {
  try {
    const res = await fetch("/api/auth/logout", { method: "POST", credentials: "same-origin" });
    return res.status === 204 || res.status === 401;
  } catch {
    return false;
  }
}

export const LOGIN_URL = "/api/auth/google/start";

import { createContext, createElement, useContext, useEffect, useState, type ReactNode } from "react";
import { parseAccount, type Account } from "../api";

/** Who is reading a public blog page: nobody signed in, or an account in any state. "loading" until the one question is answered. */
export type Reader = { kind: "loading" } | { kind: "anonymous" } | { kind: "account"; account: Account };

/**
 * The public pages are served to anyone, and this is the only thing they ask about the reader: one GET /api/me, so a signed-in
 * reader sees "Back to Antares" where a visitor sees "Sign in". It writes nothing (unlike the app's own session load, which
 * clears drafts), and a failure of any kind leaves the reader a visitor.
 */
function useAsk(): Reader {
  const [r, setR] = useState<Reader>({ kind: "loading" });
  useEffect(() => {
    const ctl = new AbortController();
    void (async () => {
      let next: Reader = { kind: "anonymous" };
      try {
        const res = await fetch("/api/me", { credentials: "same-origin", headers: { Accept: "application/json" }, signal: ctl.signal });
        const account = res.ok ? parseAccount(await res.json().catch(() => null)) : null;
        if (account) next = { kind: "account", account };
      } catch {
        /* a visitor */
      }
      if (!ctl.signal.aborted) setR(next);
    })();
    return () => ctl.abort();
  }, []);
  return r;
}

/** An approved account: the only reader who has posts of their own to mark. */
export const isActive = (r: Reader): boolean => r.kind === "account" && r.account.state === "ACTIVE";

const Ctx = createContext<Reader | null>(null);

/** Asks once for the whole page and shares the answer, so the header and the list do not each make the call. */
export function ReaderProvider({ children }: { children: ReactNode }) {
  return createElement(Ctx.Provider, { value: useAsk() }, children);
}

/** The reader, from the page's provider; a page without one is read as a visitor who is still being asked. */
export function useReader(): Reader {
  return useContext(Ctx) ?? { kind: "loading" };
}

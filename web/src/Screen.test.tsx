import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Screen, type View } from "./Screen";
import { parseAccount, type Account, type AccountState } from "./api";

const noop = () => {};
const render = (view: View) => renderToStaticMarkup(<Screen view={view} onRefresh={noop} onSignOut={noop} />);
const account = (state: AccountState, over: Partial<Account> = {}): View => ({
  screen: "account",
  account: { email: "person@example.test", displayName: "Test Person", requestedAt: "2026-01-02T03:04:05Z", state, canAdmin: false, ...over },
});

const EXPECT: Record<Exclude<AccountState, "ACTIVE">, string> = {
  PENDING: "Waiting for administrator approval",
  REJECTED: "Your access request was not approved",
  SUSPENDED: "Your access is disabled",
  SETTING_UP: "your workspace is being prepared",
  ERROR: "its workspace is not working",
};

describe("status screens", () => {
  for (const [state, text] of Object.entries(EXPECT)) {
    it(`${state} says what happened and offers sign-out, with no chat controls`, () => {
      const html = render(account(state as AccountState));
      expect(html).toContain(text);
      expect(html).toContain("person@example.test");
      expect(html).toContain("Sign out");
      for (const banned of ["<textarea", "<input", "<script", "<iframe", "composer", "Send message"]) {
        expect(html.toLowerCase()).not.toContain(banned.toLowerCase());
      }
    });
  }

  it("pending and setting-up can be refreshed, rejected and suspended cannot", () => {
    expect(render(account("PENDING"))).toContain("Refresh");
    expect(render(account("SETTING_UP"))).toContain("Refresh");
    expect(render(account("REJECTED"))).not.toContain("Refresh");
    expect(render(account("SUSPENDED"))).not.toContain("Refresh");
  });

  it("the status screen never carries a composer, even for an active account", () => {
    const html = render(account("ACTIVE"));
    expect(html).toContain("Your workspace is ready");
    expect(html).not.toContain("<textarea");
  });

  it("login offers Google and no password field", () => {
    const html = render({ screen: "login" });
    expect(html).toContain('href="/api/auth/google/start"');
    expect(html).not.toContain("<input");
  });

  it("a failed sign-in says nothing happened; an unreachable server says status is unknown", () => {
    expect(render({ screen: "login-failed" })).toContain("you have not been signed in");
    expect(render({ screen: "unreachable" })).toContain("account status is unknown");
  });

  it("escapes account data", () => {
    const html = render(account("PENDING", { displayName: "<script>alert(1)</script>", email: 'x"><img src=x onerror=alert(1)>' }));
    expect(html).not.toContain("<script>alert");
    expect(html).not.toContain("<img src=x");
    expect(html).toContain("&lt;script&gt;");
  });
});

describe("parseAccount", () => {
  it("refuses an unknown or missing state instead of guessing one", () => {
    expect(parseAccount({ state: "SUPERUSER" })).toBeNull();
    expect(parseAccount({})).toBeNull();
    expect(parseAccount(null)).toBeNull();
    expect(parseAccount("PENDING")).toBeNull();
  });
  it("keeps only the fields the screens use", () => {
    const a = parseAccount({ state: "PENDING", email: "a@example.test", id: "u1", role: "ADMIN", tenantStatus: "READY" });
    expect(Object.keys(a!).sort()).toEqual(["canAdmin", "displayName", "email", "requestedAt", "state"]);
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ActionPanel, AdminScreens, AuditTable, DECISIONS, Forbidden, REQUEUES, UserDetail, UserList, matches, when } from "./Admin";
import { AUDIT_PAGE, decide, describeAdminCode, getUser, isForbidden, listAudit, listUsers, parseAdminUser, parseUsers, requeue, type AdminUser } from "./adminApi";
import { isAdminPath, parseAdminPath } from "./route";
import { parseAccount } from "./api";

const ID = "0123456789abcdef0123456789abcdef";
const user = (over: Partial<AdminUser> = {}): AdminUser => ({
  id: ID, email: "person@example.test", displayName: "Test Person", role: "USER", approvalStatus: "PENDING", tenantStatus: "UNPROVISIONED", tenantDetail: "",
  tenantAttempts: 0, tenantCheckedAt: "", createdAt: "2026-01-02T03:04:05Z", approvedAt: "", lastLoginAt: "", binding: null, ...over,
});
const noop = () => {};
const detail = (u: AdminUser) => renderToStaticMarkup(<UserDetail user={u} onDecide={async () => ""} onRequeue={async () => ""} />);

afterEach(() => vi.unstubAllGlobals());

describe("role: who sees the admin area", () => {
  it("canAdmin comes only from the server's /api/me and defaults to false", () => {
    const base = { state: "ACTIVE", email: "a@example.test" };
    expect(parseAccount({ ...base, canAdmin: true })?.canAdmin).toBe(true);
    expect(parseAccount(base)?.canAdmin).toBe(false);
    expect(parseAccount({ ...base, canAdmin: "true" })?.canAdmin).toBe(false);
  });

  it("the forbidden state is a visible explanation, not a blank page", () => {
    const html = renderToStaticMarkup(<Forbidden />);
    expect(html).toContain("Administrators only");
    expect(html).toContain('role="alert"');
  });

  it("a 403 from any load becomes the forbidden state; other failures stay errors", () => {
    expect(isForbidden({ status: 403 })).toBe(true);
    expect(isForbidden({ status: 500 })).toBe(false);
    expect(isForbidden({ status: 0 })).toBe(false);
  });

  it("every screen starts in a labelled loading state", () => {
    for (const route of [{ kind: "users" }, { kind: "user", id: ID }, { kind: "audit" }] as const) {
      const html = renderToStaticMarkup(<AdminScreens route={route} onNavigate={noop} />);
      expect(html).toContain("Loading");
      expect(html).toContain('aria-busy="true"');
    }
    expect(renderToStaticMarkup(<AdminScreens route={{ kind: "unknown" }} onNavigate={noop} />)).toContain("No such admin page");
  });
});

describe("routes", () => {
  it("only well-formed paths reach a screen", () => {
    expect(parseAdminPath("/admin")).toEqual({ kind: "users" });
    expect(parseAdminPath("/admin/users/")).toEqual({ kind: "users" });
    expect(parseAdminPath("/admin/audit")).toEqual({ kind: "audit" });
    expect(parseAdminPath(`/admin/users/${ID}`)).toEqual({ kind: "user", id: ID });
    for (const bad of ["/admin/users/abc", `/admin/users/${ID}/approve`, "/admin/system", `/admin/users/${ID.toUpperCase()}`, "/admin/users/../audit"]) {
      expect(parseAdminPath(bad), bad).toEqual({ kind: "unknown" });
    }
    expect(isAdminPath("/admin")).toBe(true);
    expect(isAdminPath("/administrator")).toBe(false);
  });
});

describe("user list", () => {
  const users = [
    user({ id: "a".repeat(32), approvalStatus: "PENDING" }),
    user({ id: "b".repeat(32), email: "b@example.test", displayName: "", approvalStatus: "APPROVED", tenantStatus: "FAILED", tenantAttempts: 3, tenantCheckedAt: "2026-02-03T04:05:06Z" }),
    user({ id: "c".repeat(32), email: "c@example.test", approvalStatus: "APPROVED", tenantStatus: "READY", role: "ADMIN" }),
  ];
  const list = (filter: "all" | "pending" | "approved" | "failed", us = users, hasMore = false) =>
    renderToStaticMarkup(<UserList users={us} filter={filter} onFilter={noop} onOpen={noop} hasMore={hasMore} loadingMore={false} onMore={noop} />);

  it("filters pending, approved and failed", () => {
    expect(users.filter((u) => matches(u, "pending")).length).toBe(1);
    expect(users.filter((u) => matches(u, "approved")).length).toBe(2);
    expect(users.filter((u) => matches(u, "failed")).map((u) => u.email)).toEqual(["b@example.test"]);
    const html = list("failed");
    expect(html).toContain("b@example.test");
    expect(html).not.toContain("c@example.test");
  });

  it("shows role, approval, tenant status and last attempt, and links to the detail", () => {
    const html = list("all");
    for (const t of ["Admin", "Pending", "Failed", "Ready", "3 attempts", "2026-02-03 04:05 UTC"]) expect(html).toContain(t);
    expect(html).toContain(`href="/admin/users/${"b".repeat(32)}"`);
  });

  it("says so when empty, when a filter matches nothing, and when more pages exist", () => {
    expect(list("all", [])).toContain("No accounts yet.");
    expect(list("failed", [users[0]])).toContain("No accounts match this filter.");
    expect(list("all", users, true)).toContain("loaded so far");
    expect(list("all", users, true)).toContain("Load more accounts");
  });

  it("escapes account data", () => {
    const html = list("all", [user({ displayName: "<script>alert(1)</script>", email: 'x"><img src=x onerror=alert(1)>' })]);
    expect(html).not.toContain("<script>alert");
    expect(html).not.toContain("<img");
  });
});

describe("user detail", () => {
  it("shows the failure detail verbatim, escaped, in preformatted text", () => {
    const detailText = "profile probe failed:\n  <b>step 3</b> & more";
    const html = detail(user({ approvalStatus: "APPROVED", tenantStatus: "FAILED", tenantDetail: detailText, tenantAttempts: 5 }));
    expect(html).toContain("profile probe failed:\n  &lt;b&gt;step 3&lt;/b&gt; &amp; more");
    expect(html).not.toContain("<b>step 3");
  });

  it("does not invent a reason when a failed workspace has no detail", () => {
    expect(detail(user({ approvalStatus: "APPROVED", tenantStatus: "FAILED" }))).toContain("recorded no detail");
  });

  it("offers only the decisions that fit the state, and both requeues", () => {
    const labels = (s: string) => ["Approve", "Reject", "Suspend", "Reactivate"].filter((l) => detail(user({ approvalStatus: s })).includes(`>${l}<`));
    expect(labels("PENDING")).toEqual(["Approve", "Reject"]);
    expect(labels("REJECTED")).toEqual(["Approve"]);
    expect(labels("APPROVED")).toEqual(["Suspend"]);
    expect(labels("SUSPENDED")).toEqual(["Reactivate"]);
    const html = detail(user({ approvalStatus: "APPROVED" }));
    expect(html).toContain("Requeue provisioning");
    expect(html).toContain("Requeue verification");
  });

  it("no action runs from the detail screen itself: nothing is confirmed until the panel is", () => {
    expect(detail(user())).not.toContain("Confirm:");
  });

  it("every action states what it does before it runs", () => {
    const picks = [
      ...(Object.keys(DECISIONS) as (keyof typeof DECISIONS)[]).map((action) => ({ pick: { kind: "decision", action } as const, effect: DECISIONS[action].effect })),
      ...(Object.keys(REQUEUES) as (keyof typeof REQUEUES)[]).map((action) => ({ pick: { kind: "requeue", action } as const, effect: REQUEUES[action].effect })),
    ];
    for (const { pick, effect } of picks) {
      const html = renderToStaticMarkup(<ActionPanel pick={pick} busy={false} error="" onCancel={noop} onConfirm={noop} />);
      expect(html).toContain(effect);
    }
    const warned = REQUEUES["tenant-binding"].effect + REQUEUES["tenant-verify"].effect;
    expect(warned.match(/Not undone by reloading the page/g)?.length).toBe(2);
  });

  it("reject and suspend require a reason before confirm is enabled", () => {
    const html = renderToStaticMarkup(<ActionPanel pick={{ kind: "decision", action: "reject" }} busy={false} error="" onCancel={noop} onConfirm={noop} />);
    expect(html).toContain("<textarea");
    expect(html).toMatch(/<button type="submit" disabled=""/);
    const approve = renderToStaticMarkup(<ActionPanel pick={{ kind: "decision", action: "approve" }} busy={false} error="" onCancel={noop} onConfirm={noop} />);
    expect(approve).not.toContain("<textarea");
    expect(approve).not.toMatch(/<button type="submit" disabled=""/);
  });

  it("an action that fails shows the failure, never success", () => {
    const html = renderToStaticMarkup(<ActionPanel pick={{ kind: "requeue", action: "tenant-verify" }} busy={false} error={describeAdminCode("NO_BINDING", 409)} onCancel={noop} onConfirm={noop} />);
    expect(html).toContain('role="alert"');
    expect(html).toContain("no tenant binding");
  });
});

describe("audit log", () => {
  const entry = (n: number) => ({ id: `e${n}`, actor: "adm", action: "admin.approve", targetId: ID, outcome: "ok", requestId: `r${n}`, createdAt: "2026-03-04T05:06:07Z" });
  it("renders metadata columns only and paginates", () => {
    const html = renderToStaticMarkup(<AuditTable entries={Array.from({ length: AUDIT_PAGE }, (_, i) => entry(i))} offset={50} onPage={noop} />);
    for (const t of ["When", "Actor", "Action", "Target", "Outcome", "Request", "Entries 51–100", "2026-03-04 05:06 UTC"]) expect(html).toContain(t);
    expect(html).toMatch(/Newer<\/button>/);
    expect(html).not.toMatch(/disabled="">Older/);
  });
  it("first page cannot go newer; a short page cannot go older; empty says so", () => {
    const short = renderToStaticMarkup(<AuditTable entries={[entry(1)]} offset={0} onPage={noop} />);
    expect(short).toMatch(/disabled="">Newer/);
    expect(short).toMatch(/disabled="">Older/);
    expect(renderToStaticMarkup(<AuditTable entries={[]} offset={0} onPage={noop} />)).toContain("Nothing has been recorded yet.");
  });
});

describe("api calls send the server nothing it must choose", () => {
  const reply = (status: number, body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status }));
  const serverUser = { ...user({ approvalStatus: "APPROVED" }) };

  it("requeue and approve are POSTs to the id's own path with an empty body; reject carries only a reason", async () => {
    const f = reply(202, serverUser);
    vi.stubGlobal("fetch", f);
    await requeue(ID, "tenant-binding");
    await decide(ID, "approve", "");
    await decide(ID, "reject", "not a family member");
    const calls = f.mock.calls as unknown as [string, RequestInit][];
    expect(calls.map(([u, i]) => `${i.method} ${u}`)).toEqual([`POST /api/admin/users/${ID}/tenant-binding`, `POST /api/admin/users/${ID}/approve`, `POST /api/admin/users/${ID}/reject`]);
    expect(calls.map(([, i]) => JSON.parse(i.body as string))).toEqual([{}, {}, { reason: "not a family member" }]);
    for (const [, i] of calls) expect(i.credentials).toBe("same-origin");
  });

  it("reads carry no body and use bounded paging parameters", async () => {
    const f = vi.fn(async (u: string) => new Response(JSON.stringify(u.includes("audit") ? { entries: [] } : u.endsWith(ID) ? serverUser : { users: [] }), { status: 200 }));
    vi.stubGlobal("fetch", f);
    await listUsers(200);
    await listAudit(50);
    await getUser(ID);
    const urls = (f.mock.calls as unknown as [string, RequestInit][]).map(([u]) => u);
    expect(urls).toEqual(["/api/admin/users?limit=200&offset=200", "/api/admin/audit?limit=50&offset=50", `/api/admin/users/${ID}`]);
  });

  it("surfaces 403 and server codes, and treats an unreadable success as a failure", async () => {
    vi.stubGlobal("fetch", reply(403, { error: { code: "FORBIDDEN", message: "Administrator access required" } }));
    const r = await listUsers(0);
    expect(r).toEqual({ ok: false, code: "FORBIDDEN", status: 403 });
    vi.stubGlobal("fetch", reply(409, { error: { code: "STATE_CONFLICT" } }));
    const c = await decide(ID, "approve", "");
    expect(!c.ok && describeAdminCode(c.code, c.status)).toContain("state changed");
    vi.stubGlobal("fetch", reply(200, { users: [{ nope: true }] }));
    expect((await listUsers(0)).ok).toBe(false);
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("down"))));
    expect(await getUser(ID)).toEqual({ ok: false, code: "NETWORK", status: 0 });
  });

  it("the server's own message text is never shown", () => {
    expect(describeAdminCode("INTERNAL", 500)).not.toContain("Administrator access required");
    expect(describeAdminCode("anything", 500)).toContain("Something went wrong");
  });

  it("parsing takes only the closed set of fields", () => {
    const parsed = parseAdminUser({ ...serverUser, profileName: "secret-profile", apiKey: "k" });
    expect(parsed && Object.keys(parsed)).not.toContain("profileName");
    expect(JSON.stringify(parsed)).not.toContain("secret-profile");
    expect(parseAdminUser({ ...serverUser, id: "../x" })).toBeNull();
    expect(parseUsers({ users: [serverUser] })).toHaveLength(1);
  });
});

describe("timestamps", () => {
  it("are fixed-format UTC, blank is a dash, junk is shown as received", () => {
    expect(when("2026-01-02T03:04:05Z")).toBe("2026-01-02 03:04 UTC");
    expect(when("")).toBe("—");
    expect(when("not a date")).toBe("not a date");
  });
});

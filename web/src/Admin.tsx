import { useCallback, useEffect, useState, type FormEvent, type MouseEvent, type ReactNode } from "react";
import {
  AUDIT_PAGE,
  USERS_PAGE,
  decide,
  describeAdminCode,
  getUser,
  isForbidden,
  listAudit,
  listUsers,
  requeue,
  type AdminResult,
  type AdminUser,
  type AuditEntry,
  type Decision,
  type Requeue,
} from "./adminApi";
import { userPath, type AdminRoute } from "./route";
import { AntaresMark } from "./brand/AntaresMark";

// ---- load state: every screen renders all four of these ----------------------

export type Load<T> = { kind: "loading" } | { kind: "ready"; value: T } | { kind: "error"; message: string } | { kind: "forbidden" };

function useLoad<T>(fetcher: (signal: AbortSignal) => Promise<AdminResult<T>>, key: string): [Load<T>, () => void] {
  const [state, setState] = useState<Load<T>>({ kind: "loading" });
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const ctl = new AbortController();
    setState({ kind: "loading" });
    void fetcher(ctl.signal).then((r) => {
      if (ctl.signal.aborted) return;
      if (r.ok) setState({ kind: "ready", value: r.value });
      else setState(isForbidden(r) ? { kind: "forbidden" } : { kind: "error", message: describeAdminCode(r.code, r.status) });
    });
    return () => ctl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, tick]);
  return [state, useCallback(() => setTick((t) => t + 1), [])];
}

export function Forbidden() {
  return (
    <section aria-labelledby="forbidden-h">
      <h1 id="forbidden-h">Administrators only</h1>
      <p className="notice" role="alert">
        This account is not allowed to use the admin area. The server decided this; nothing on this page was loaded.
      </p>
      <p className="label">If you should have access, ask the person who runs this service.</p>
    </section>
  );
}

function Problem({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <p className="notice" role="alert">
      {message}{" "}
      <button type="button" className="quiet" onClick={onRetry}>
        Try again
      </button>
    </p>
  );
}

function Loading({ what }: { what: string }) {
  return (
    <p className="label" role="status" aria-busy="true">
      Loading {what}…
    </p>
  );
}

// ---- formatting ---------------------------------------------------------------

/** Deterministic UTC, so a screenshot, a test and a user in another zone all read the same. */
export function when(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : `${d.toISOString().slice(0, 16).replace("T", " ")} UTC`;
}

const WORDS: Record<string, string> = {
  PENDING: "Pending",
  APPROVED: "Approved",
  REJECTED: "Rejected",
  SUSPENDED: "Suspended",
  UNPROVISIONED: "Not provisioned",
  CONFIGURING: "Configuring",
  READY: "Ready",
  FAILED: "Failed",
  DISABLED: "Disabled",
};
const word = (s: string) => WORDS[s] ?? (s || "—");

/** The status is the word; the class only adds weight, so colour is never the signal. */
function Badge({ value, bad }: { value: string; bad?: boolean }) {
  return <span className={bad ? "badge bad" : "badge"}>{word(value)}</span>;
}

// ---- shell ----------------------------------------------------------------------

export function AdminShell({ email, route, onNavigate, onSignOut, children }: { email: string; route: AdminRoute; onNavigate: (path: string) => void; onSignOut: () => void; children: ReactNode }) {
  const link = (path: string, label: string, current: boolean) => (
    <a href={path} className={current ? "navlink current" : "navlink"} aria-current={current ? "page" : undefined} onClick={(e) => go(e, onNavigate, path)}>
      {label}
    </a>
  );
  useEffect(() => {
    document.body.classList.add("admin");
    return () => document.body.classList.remove("admin");
  }, []);
  return (
    <div className="admin-app">
      <header className="bar">
        <strong className="brand">
          <AntaresMark size={20} />
          Antares
        </strong>
        <nav className="admin-nav" aria-label="Admin">
          {link("/admin/users", "Users", route.kind === "users" || route.kind === "user")}
          {link("/admin/audit", "Audit log", route.kind === "audit")}
          {link("/", "Back to chat", false)}
        </nav>
        <span className="who-am-i">{email}</span>
        <button type="button" className="quiet" onClick={onSignOut}>
          Sign out
        </button>
      </header>
      <main className="admin-main">{children}</main>
    </div>
  );
}

/** Plain left-clicks navigate in-app; modified clicks keep the browser's own behaviour. */
function go(e: MouseEvent, onNavigate: (path: string) => void, path: string) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  onNavigate(path);
}

// ---- user list ------------------------------------------------------------------

export type Filter = "all" | "pending" | "approved" | "failed";
const FILTERS: { id: Filter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "pending", label: "Pending" },
  { id: "approved", label: "Approved" },
  { id: "failed", label: "Failed" },
];

export const matches = (u: AdminUser, f: Filter): boolean =>
  f === "all" || (f === "pending" && u.approvalStatus === "PENDING") || (f === "approved" && u.approvalStatus === "APPROVED") || (f === "failed" && u.tenantStatus === "FAILED");

export function UserList({ users, filter, onFilter, onOpen, hasMore, loadingMore, onMore }: { users: AdminUser[]; filter: Filter; onFilter: (f: Filter) => void; onOpen: (path: string) => void; hasMore: boolean; loadingMore: boolean; onMore: () => void }) {
  const shown = users.filter((u) => matches(u, filter));
  return (
    <section aria-labelledby="users-h">
      <h1 id="users-h">Users</h1>
      <div className="filters" role="group" aria-label="Filter users">
        {FILTERS.map((f) => (
          <button key={f.id} type="button" className={filter === f.id ? "quiet on" : "quiet"} aria-pressed={filter === f.id} onClick={() => onFilter(f.id)}>
            {f.label} ({users.filter((u) => matches(u, f.id)).length})
          </button>
        ))}
      </div>
      {hasMore && <p className="label">Counts and filters cover the {users.length} accounts loaded so far.</p>}
      {users.length === 0 && <p className="label">No accounts yet.</p>}
      {users.length > 0 && shown.length === 0 && <p className="label">No accounts match this filter.</p>}
      {shown.length > 0 && (
        <div className="table-wrap">
          <table className="admin-table">
            <thead>
              <tr>
                <th scope="col">User</th>
                <th scope="col">Role</th>
                <th scope="col">Approval</th>
                <th scope="col">Tenant</th>
                <th scope="col">Last attempt</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((u) => (
                <tr key={u.id}>
                  <th scope="row">
                    <a href={userPath(u.id)} onClick={(e) => go(e, onOpen, userPath(u.id))}>
                      {u.displayName || u.email}
                    </a>
                    {u.displayName && <span className="sub">{u.email}</span>}
                  </th>
                  <td>{u.role === "ADMIN" ? "Admin" : "User"}</td>
                  <td>
                    <Badge value={u.approvalStatus} />
                  </td>
                  <td>
                    <Badge value={u.tenantStatus} bad={u.tenantStatus === "FAILED"} />
                  </td>
                  <td>{u.tenantCheckedAt ? `${when(u.tenantCheckedAt)}${u.tenantAttempts ? ` · ${u.tenantAttempts} attempt${u.tenantAttempts === 1 ? "" : "s"}` : ""}` : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {hasMore && (
        <button type="button" className="quiet" disabled={loadingMore} onClick={onMore}>
          {loadingMore ? "Loading…" : "Load more accounts"}
        </button>
      )}
    </section>
  );
}

export function UsersScreen({ onOpen }: { onOpen: (path: string) => void }) {
  const [state, reload] = useLoad((s) => listUsers(0, s), "users");
  const [more, setMore] = useState<AdminUser[]>([]);
  const [moreState, setMoreState] = useState<"idle" | "loading" | "error" | "done">("idle");
  const [filter, setFilter] = useState<Filter>("all");

  if (state.kind === "loading") return <Loading what="users" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <Problem message={state.message} onRetry={reload} />;
  const users = [...state.value, ...more];
  const hasMore = moreState !== "done" && users.length > 0 && users.length % USERS_PAGE === 0;
  const loadMore = () => {
    setMoreState("loading");
    void listUsers(users.length).then((r) => {
      if (!r.ok) return setMoreState("error");
      setMore((m) => [...m, ...r.value]);
      setMoreState(r.value.length < USERS_PAGE ? "done" : "idle");
    });
  };
  return (
    <>
      <UserList users={users} filter={filter} onFilter={setFilter} onOpen={onOpen} hasMore={hasMore} loadingMore={moreState === "loading"} onMore={loadMore} />
      {moreState === "error" && <Problem message="Could not load more accounts." onRetry={loadMore} />}
    </>
  );
}

// ---- user detail ----------------------------------------------------------------

interface ActionCopy {
  label: string;
  /** What running it does, shown before it runs. */
  effect: string;
  needsReason?: boolean;
}

export const DECISIONS: Record<Decision, ActionCopy> = {
  approve: { label: "Approve", effect: "Marks the account approved and starts provisioning its private workspace. The person can chat once the workspace is ready; until then they see a setting-up screen." },
  reject: { label: "Reject", effect: "Refuses access. The person sees that their request was not approved and cannot chat. The reason is stored in the audit log.", needsReason: true },
  suspend: { label: "Suspend", effect: "Disables the account. The person is blocked from chat straight away. The reason is stored in the audit log.", needsReason: true },
  reactivate: { label: "Reactivate", effect: "Returns a suspended account to approved and starts provisioning again if the workspace needs it." },
};

export const REQUEUES: Record<Requeue, ActionCopy> = {
  "tenant-binding": {
    label: "Requeue provisioning",
    effect: "Clears the recorded failure detail and the attempt count, then queues provisioning of the workspace again. The failure text above will no longer be shown, so read it first. Not undone by reloading the page.",
  },
  "tenant-verify": {
    label: "Requeue verification",
    effect: "Clears the recorded failure detail and the attempt count, then re-runs the checks on the existing binding. Needs a binding: the server refuses it for an account that has none. Not undone by reloading the page.",
  },
};

/** Which decisions are worth offering for an approval state. A convenience only: the server re-checks every one. */
const OFFER: Record<string, Decision[]> = { PENDING: ["approve", "reject"], REJECTED: ["approve"], APPROVED: ["suspend"], SUSPENDED: ["reactivate"] };

type Pick = { kind: "decision"; action: Decision } | { kind: "requeue"; action: Requeue };

export function ActionPanel({ pick, busy, error, onCancel, onConfirm }: { pick: Pick; busy: boolean; error: string; onCancel: () => void; onConfirm: (reason: string) => void }) {
  const copy = pick.kind === "decision" ? DECISIONS[pick.action] : REQUEUES[pick.action];
  const [reason, setReason] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onConfirm(reason.trim());
  };
  const blocked = busy || (!!copy.needsReason && reason.trim() === "");
  return (
    <form className="confirm" onSubmit={submit} aria-label={`Confirm: ${copy.label}`}>
      <h3>{copy.label}</h3>
      <p>{copy.effect}</p>
      {copy.needsReason && (
        <>
          <label htmlFor="reason">Reason (required, recorded in the audit log)</label>
          <textarea id="reason" value={reason} maxLength={500} rows={3} onChange={(e) => setReason(e.target.value)} />
        </>
      )}
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
      <button type="submit" disabled={blocked}>
        {busy ? "Working…" : `Confirm: ${copy.label}`}
      </button>
      <button type="button" className="quiet" disabled={busy} onClick={onCancel}>
        Cancel
      </button>
    </form>
  );
}

export function UserDetail({ user, onDecide, onRequeue }: { user: AdminUser; onDecide: (a: Decision, reason: string) => Promise<string>; onRequeue: (m: Requeue) => Promise<string> }) {
  const [pick, setPick] = useState<Pick | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const choose = (p: Pick) => {
    setPick(p);
    setError("");
    setDone("");
  };
  const confirm = async (reason: string) => {
    if (!pick) return;
    setBusy(true);
    const err = pick.kind === "decision" ? await onDecide(pick.action, reason) : await onRequeue(pick.action);
    setBusy(false);
    setError(err);
    if (!err) {
      setDone(`${(pick.kind === "decision" ? DECISIONS[pick.action] : REQUEUES[pick.action]).label}: sent. The state above is what the server now reports.`);
      setPick(null);
    }
  };
  const offered = OFFER[user.approvalStatus] ?? [];
  const failed = user.tenantStatus === "FAILED";
  return (
    <section aria-labelledby="user-h">
      <h1 id="user-h">{user.displayName || user.email}</h1>
      <dl className="facts">
        <dt>Email</dt>
        <dd>{user.email}</dd>
        <dt>Role</dt>
        <dd>{user.role === "ADMIN" ? "Admin" : "User"}</dd>
        <dt>Approval</dt>
        <dd>
          <Badge value={user.approvalStatus} />
        </dd>
        <dt>Requested</dt>
        <dd>{when(user.createdAt)}</dd>
        <dt>Approved</dt>
        <dd>{when(user.approvedAt)}</dd>
        <dt>Last sign-in</dt>
        <dd>{when(user.lastLoginAt)}</dd>
      </dl>

      <h2>Workspace</h2>
      <dl className="facts">
        <dt>Tenant status</dt>
        <dd>
          <Badge value={user.tenantStatus} bad={failed} />
        </dd>
        <dt>Binding</dt>
        <dd>{user.binding ? `${word(user.binding.status)}${user.binding.verifiedAt ? ` · verified ${when(user.binding.verifiedAt)}` : ""}` : "None"}</dd>
        <dt>Attempts</dt>
        <dd>{user.tenantAttempts}</dd>
        <dt>Last checked</dt>
        <dd>{when(user.tenantCheckedAt)}</dd>
      </dl>
      {user.tenantDetail ? (
        <div className="failure">
          <p className="label">Failure detail, as recorded</p>
          <pre className="plain">{user.tenantDetail}</pre>
        </div>
      ) : (
        failed && <p className="notice">The workspace failed, and the server recorded no detail.</p>
      )}

      <h2>Decision</h2>
      {offered.length === 0 && <p className="label">No decision applies to this account in its current state.</p>}
      <div className="actions">
        {offered.map((a) => (
          <button key={a} type="button" className={a === "approve" || a === "reactivate" ? undefined : "quiet"} onClick={() => choose({ kind: "decision", action: a })}>
            {DECISIONS[a].label}
          </button>
        ))}
      </div>

      <h2>Provisioning</h2>
      <div className="actions">
        {(Object.keys(REQUEUES) as Requeue[]).map((m) => (
          <button key={m} type="button" className="quiet" onClick={() => choose({ kind: "requeue", action: m })}>
            {REQUEUES[m].label}
          </button>
        ))}
      </div>
      <p className="label">Both are refused by the server unless the account is approved.</p>

      {pick && <ActionPanel key={`${pick.kind}-${pick.action}`} pick={pick} busy={busy} error={error} onCancel={() => setPick(null)} onConfirm={(r) => void confirm(r)} />}
      {done && (
        <p className="label" role="status">
          {done}
        </p>
      )}
    </section>
  );
}

export function UserScreen({ id }: { id: string }) {
  const [state, reload] = useLoad((s) => getUser(id, s), `user-${id}`);
  const [override, setOverride] = useState<AdminUser | null>(null);
  useEffect(() => setOverride(null), [id]);
  if (state.kind === "loading") return <Loading what="account" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <Problem message={state.message} onRetry={reload} />;
  const user = override ?? state.value;
  // The action's response is the server's new view of the account: show it, do not compute one.
  const after = async (p: Promise<AdminResult<AdminUser>>): Promise<string> => {
    const r = await p;
    if (!r.ok) return describeAdminCode(r.code, r.status);
    setOverride(r.value);
    return "";
  };
  return <UserDetail user={user} onDecide={(a, reason) => after(decide(id, a, reason))} onRequeue={(m) => after(requeue(id, m))} />;
}

// ---- audit log ------------------------------------------------------------------

export function AuditTable({ entries, offset, onPage }: { entries: AuditEntry[]; offset: number; onPage: (offset: number) => void }) {
  return (
    <section aria-labelledby="audit-h">
      <h1 id="audit-h">Audit log</h1>
      <p className="label">Who did what to which account, and when. Metadata only: no conversation content is ever recorded here.</p>
      {entries.length === 0 && <p className="label">{offset === 0 ? "Nothing has been recorded yet." : "No more entries."}</p>}
      {entries.length > 0 && (
        <div className="table-wrap">
          <table className="admin-table">
            <thead>
              <tr>
                <th scope="col">When</th>
                <th scope="col">Actor</th>
                <th scope="col">Action</th>
                <th scope="col">Target</th>
                <th scope="col">Outcome</th>
                <th scope="col">Request</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.id}>
                  <td>{when(e.createdAt)}</td>
                  <td className="mono">{e.actor || "—"}</td>
                  <td>{e.action}</td>
                  <td className="mono">{e.targetId || "—"}</td>
                  <td>{e.outcome}</td>
                  <td className="mono">{e.requestId || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <nav className="pager" aria-label="Audit pages">
        <button type="button" className="quiet" disabled={offset === 0} onClick={() => onPage(Math.max(0, offset - AUDIT_PAGE))}>
          Newer
        </button>
        <span className="label">
          {entries.length > 0 ? `Entries ${offset + 1}–${offset + entries.length}` : "Empty page"}
        </span>
        <button type="button" className="quiet" disabled={entries.length < AUDIT_PAGE} onClick={() => onPage(offset + AUDIT_PAGE)}>
          Older
        </button>
      </nav>
    </section>
  );
}

export function AuditScreen() {
  const [offset, setOffset] = useState(0);
  const [state, reload] = useLoad((s) => listAudit(offset, s), `audit-${offset}`);
  if (state.kind === "loading") return <Loading what="the audit log" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <Problem message={state.message} onRetry={reload} />;
  return <AuditTable entries={state.value} offset={offset} onPage={setOffset} />;
}

// ---- router ---------------------------------------------------------------------

export function NotFound() {
  return (
    <section>
      <h1>No such admin page</h1>
      <p className="label">Try the user list or the audit log above.</p>
    </section>
  );
}

export function AdminScreens({ route, onNavigate }: { route: AdminRoute; onNavigate: (path: string) => void }) {
  switch (route.kind) {
    case "users":
      return <UsersScreen onOpen={onNavigate} />;
    case "user":
      return <UserScreen id={route.id} />;
    case "audit":
      return <AuditScreen />;
    case "unknown":
      return <NotFound />;
  }
}

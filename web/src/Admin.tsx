import { useCallback, useEffect, useRef, useState, type FormEvent, type MouseEvent, type ReactNode } from "react";
import {
  USERS_PAGE,
  decide,
  describeAdminCode,
  getKenariUsage,
  getToolPolicy,
  getUser,
  isForbidden,
  listAudit,
  listUsers,
  requeue,
  setToolPolicy,
  type AdminResult,
  type AdminUser,
  type AuditEntry,
  type Decision,
  type KenariUsage,
  type KenariUsageView,
  type UsageSection,
  type Requeue,
  type ToolPolicy,
  loadAllAudit,
} from "./adminApi";
import { getCapabilities, type Inventory } from "./chatApi";
import { userPath, type AdminRoute } from "./route";
import { saveText } from "./exportChat";
import { AntaresMark } from "./brand/AntaresMark";
import { QuotaBar, WINDOW_LABELS, grouped } from "./QuotaBar";

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
          {link("/admin/capabilities", "Capabilities", route.kind === "capabilities")}
          {link("/admin/usage", "Provider usage", route.kind === "usage")}
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
  { id: "failed", label: "Failed workspace" },
];

/** The approval status the server filters on. "failed" is a tenant state, which the API cannot filter, so it asks for everyone. */
export const serverStatus = (f: Filter): string => (f === "pending" ? "PENDING" : f === "approved" ? "APPROVED" : "");

/** What the server does not narrow for this filter, applied to the rows it returned. */
export const matches = (u: AdminUser, f: Filter): boolean => f !== "failed" || u.tenantStatus === "FAILED";

export function UserControls({ filter, onFilter, search, onSearch }: { filter: Filter; onFilter: (f: Filter) => void; search: string; onSearch: (q: string) => void }) {
  const [text, setText] = useState(search);
  return (
    <>
      <form
        className="search"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          onSearch(text.trim());
        }}
      >
        <label htmlFor="user-search" className="sr">
          Search by name or email
        </label>
        <input id="user-search" type="search" value={text} maxLength={100} placeholder="Search name or email" onChange={(e) => setText(e.target.value)} />
        <button type="submit" className="quiet">
          Search
        </button>
        {search && (
          <button
            type="button"
            className="quiet"
            onClick={() => {
              setText("");
              onSearch("");
            }}
          >
            Clear
          </button>
        )}
      </form>
      <div className="filters" role="group" aria-label="Filter users">
        {FILTERS.map((f) => (
          <button key={f.id} type="button" className={filter === f.id ? "quiet on" : "quiet"} aria-pressed={filter === f.id} onClick={() => onFilter(f.id)}>
            {f.label}
          </button>
        ))}
      </div>
      {filter === "failed" && <p className="label">Failed workspace is a tenant state the server cannot filter on, so it is applied to the accounts loaded so far. Load more to look further.</p>}
    </>
  );
}

export function UserList({ users, filter, search, onOpen, hasMore, loadingMore, onMore }: { users: AdminUser[]; filter: Filter; search: string; onOpen: (path: string) => void; hasMore: boolean; loadingMore: boolean; onMore: () => void }) {
  const shown = users.filter((u) => matches(u, filter));
  return (
    <>
      {users.length === 0 && <p className="label">{search || filter !== "all" ? "No accounts match." : "No accounts yet."}</p>}
      {users.length > 0 && shown.length === 0 && <p className="label">None of the {users.length} accounts loaded so far match this filter.</p>}
      {shown.length > 0 && (
        <div className="table-wrap">
          {/* On a phone this table is shown as cards (styles.css, .user-table). The explicit roles keep it a table for
              assistive technology whatever the display, the header row stays in the accessibility tree (only visually
              hidden), and each cell carries its column name as real text, shown on the card. */}
          <table className="admin-table user-table" role="table">
            <thead role="rowgroup">
              <tr role="row">
                <th scope="col" role="columnheader">User</th>
                <th scope="col" role="columnheader">Role</th>
                <th scope="col" role="columnheader">Approval</th>
                <th scope="col" role="columnheader">Tenant</th>
                <th scope="col" role="columnheader">Last attempt</th>
              </tr>
            </thead>
            <tbody role="rowgroup">
              {shown.map((u) => (
                <tr key={u.id} role="row">
                  <th scope="row" role="rowheader">
                    <a href={userPath(u.id)} onClick={(e) => go(e, onOpen, userPath(u.id))}>
                      {u.displayName || u.email}
                    </a>
                    {u.displayName && <span className="sub">{u.email}</span>}
                  </th>
                  <td role="cell">
                    <span className="cell-label">Role</span>
                    {u.role === "ADMIN" ? "Admin" : "User"}
                  </td>
                  <td role="cell">
                    <span className="cell-label">Approval</span>
                    <Badge value={u.approvalStatus} />
                  </td>
                  <td role="cell">
                    <span className="cell-label">Tenant</span>
                    <Badge value={u.tenantStatus} bad={u.tenantStatus === "FAILED"} />
                  </td>
                  <td role="cell" className="when">
                    <span className="cell-label">Last attempt</span>
                    {u.tenantCheckedAt ? `${when(u.tenantCheckedAt)}${u.tenantAttempts ? ` · ${u.tenantAttempts} attempt${u.tenantAttempts === 1 ? "" : "s"}` : ""}` : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {users.length > 0 && <p className="label">{hasMore ? `${users.length} accounts loaded; more exist.` : `${users.length} account${users.length === 1 ? "" : "s"}.`}</p>}
      {hasMore && (
        <button type="button" className="quiet" disabled={loadingMore} onClick={onMore}>
          {loadingMore ? "Loading…" : "Load more accounts"}
        </button>
      )}
    </>
  );
}

/** One result set for one (status, search) pair. It is keyed by that pair, so a new query starts from scratch. */
function UsersResults({ filter, search, onOpen }: { filter: Filter; search: string; onOpen: (path: string) => void }) {
  const status = serverStatus(filter);
  const [state, reload] = useLoad((s) => listUsers(0, status, search, s), "first");
  const [more, setMore] = useState<AdminUser[]>([]);
  const [moreState, setMoreState] = useState<"idle" | "loading" | "error" | "done">("idle");
  if (state.kind === "loading") return <Loading what="users" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <Problem message={state.message} onRetry={reload} />;
  const users = [...state.value, ...more];
  const hasMore = moreState !== "done" && users.length > 0 && users.length % USERS_PAGE === 0;
  const loadMore = () => {
    setMoreState("loading");
    void listUsers(users.length, status, search).then((r) => {
      if (!r.ok) return setMoreState("error");
      setMore((m) => [...m, ...r.value]);
      setMoreState(r.value.length < USERS_PAGE ? "done" : "idle");
    });
  };
  return (
    <>
      <UserList users={users} filter={filter} search={search} onOpen={onOpen} hasMore={hasMore} loadingMore={moreState === "loading"} onMore={loadMore} />
      {moreState === "error" && <Problem message="Could not load more accounts." onRetry={loadMore} />}
    </>
  );
}

export function UsersScreen({ onOpen }: { onOpen: (path: string) => void }) {
  const [filter, setFilter] = useState<Filter>("all");
  const [search, setSearch] = useState("");
  return (
    <section aria-labelledby="users-h">
      <h1 id="users-h">Users</h1>
      <UserControls filter={filter} onFilter={setFilter} search={search} onSearch={setSearch} />
      <UsersResults key={`${serverStatus(filter)}|${search}`} filter={filter} search={search} onOpen={onOpen} />
    </section>
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
  "revoke-sessions": {
    label: "Sign out all sessions",
    effect: "Ends every session of this account on every device, stops its alerts and closes any open reply stream. The account, its chats and its workspace stay as they are; the person signs in again. Recorded in the audit log.",
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
  // The panel appears below the button that opened it: move focus into it so a keyboard or screen-reader
  // user lands on the form they now have to answer instead of staying on the trigger above.
  const form = useRef<HTMLFormElement>(null);
  useEffect(() => form.current?.focus(), []);
  const blocked = busy || (!!copy.needsReason && reason.trim() === "");
  return (
    <form ref={form} tabIndex={-1} className="confirm" onSubmit={submit} aria-label={`Confirm: ${copy.label}`}>
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
        {(["tenant-binding", "tenant-verify"] as Requeue[]).map((m) => (
          <button key={m} type="button" className="quiet" onClick={() => choose({ kind: "requeue", action: m })}>
            {REQUEUES[m].label}
          </button>
        ))}
      </div>
      <p className="label">Both are refused by the server unless the account is approved.</p>

      <h2>Sessions</h2>
      <div className="actions">
        <button type="button" className="quiet" onClick={() => choose({ kind: "requeue", action: "revoke-sessions" })}>
          {REQUEUES["revoke-sessions"].label}
        </button>
      </div>

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
  // AdminScreens keys this component by id, so state never outlives the account it was loaded for.
  const [override, setOverride] = useState<AdminUser | null>(null);
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

export function AuditTable({ entries, hasOlder, hasNewer, onOlder, onNewer }: { entries: AuditEntry[]; hasOlder: boolean; hasNewer: boolean; onOlder: () => void; onNewer: () => void }) {
  const newest = entries[0]?.createdAt;
  const oldest = entries[entries.length - 1]?.createdAt;
  return (
    <section aria-labelledby="audit-h">
      <h1 id="audit-h">Audit log</h1>
      <p className="label">Who did what to which account, and when. Metadata only: no conversation content is ever recorded here.</p>
      {entries.length === 0 && <p className="label">{hasNewer ? "No more entries." : "Nothing has been recorded yet."}</p>}
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
        <button type="button" className="quiet" disabled={!hasNewer} onClick={onNewer}>
          Newer
        </button>
        <span className="label">{entries.length > 0 ? `${entries.length} entr${entries.length === 1 ? "y" : "ies"}, ${when(newest)} back to ${when(oldest)}` : "Empty page"}</span>
        <button type="button" className="quiet" disabled={!hasOlder} onClick={onOlder}>
          Older
        </button>
      </nav>
    </section>
  );
}

export function AuditScreen() {
  // The cursors of the pages already visited, newest page first. "Newer" pops one, so it re-reads
  // from the same position instead of guessing a backwards offset in a feed that keeps growing.
  const [trail, setTrail] = useState<string[]>([""]);
  const before = trail[trail.length - 1];
  const [state, reload] = useLoad((s) => listAudit(before, s), `audit-${trail.join(">")}`);
  if (state.kind === "loading") return <Loading what="the audit log" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <Problem message={state.message} onRetry={reload} />;
  const { entries, next } = state.value;
  return (
    <>
      <AuditExport />
      <AuditTable entries={entries} hasOlder={next !== ""} hasNewer={trail.length > 1} onOlder={() => setTrail((t) => [...t, next])} onNewer={() => setTrail((t) => t.slice(0, -1))} />
    </>
  );
}

/** Downloads the whole audit log as JSON (#113): actor, action, target, outcome, request id and time; never content. */
export function AuditExport() {
  const [state, setState] = useState<"" | "working" | "done" | string>("");
  const run = async () => {
    setState("working");
    const r = await loadAllAudit();
    if (!r.ok) {
      setState(describeAdminCode(r.code, r.status));
      return;
    }
    const now = new Date().toISOString();
    saveText(`antares-audit-${now.slice(0, 10)}.json`, JSON.stringify({ exportedAt: now, entries: r.value }, null, 2), "application/json");
    setState("done");
  };
  return (
    <p className="actions">
      <button type="button" className="quiet" disabled={state === "working"} onClick={() => void run()}>
        Export audit log (JSON)
      </button>{" "}
      <span role="status" className="label">
        {state === "working" ? "Reading every page…" : state === "done" ? "Exported. Metadata only: no conversation content." : state}
      </span>
    </p>
  );
}

export function CapabilitiesTable({ inv }: { inv: Inventory }) {
  return (
    <section aria-labelledby="caps-h">
      <h1 id="caps-h">Assistant capabilities</h1>
      <p className="label">Read live from the assistant backend. A capability it does not confirm is unsupported, and users are not offered it.</p>
      <div className="table-wrap">
        <table className="admin-table">
          <thead>
            <tr>
              <th scope="col">Capability</th>
              <th scope="col">Status</th>
              <th scope="col">Detail</th>
            </tr>
          </thead>
          <tbody>
            {inv.capabilities.map((c) => (
              <tr key={c.id}>
                <th scope="row">{c.label}</th>
                <td>
                  <span className={c.available ? "badge" : "badge bad"}>{c.available ? "Available" : "Unsupported"}</span>
                </td>
                <td>{c.available ? detailOf(c.id, inv) : c.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <h2>Toolsets on the backend</h2>
      {inv.toolsets.length === 0 ? (
        <p className="label">The backend reported no toolsets.</p>
      ) : (
        <div className="table-wrap">
          <table className="admin-table">
            <thead>
              <tr>
                <th scope="col">Toolset</th>
                <th scope="col">Status</th>
                <th scope="col">Detail</th>
              </tr>
            </thead>
            <tbody>
              {inv.toolsets.map((t) => (
                <tr key={t.name}>
                  <th scope="row">{t.name}</th>
                  <td>
                    <span className={t.available ? "badge" : "badge bad"}>{t.available ? "Available" : "Unsupported"}</span>
                  </td>
                  <td>{t.available ? `${t.tools.length} tools` : t.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function detailOf(id: string, inv: Inventory): string {
  if (id === "models") return `${inv.providers.length} provider(s), ${inv.providers.reduce((n, p) => n + p.totalModels, 0)} model(s)`;
  if (id === "skills") return `${inv.skills.length} skill(s)`;
  if (id === "toolsets") return `${inv.toolsets.filter((t) => t.available).length} of ${inv.toolsets.length} available`;
  return "";
}

export function CapabilitiesScreen() {
  const [state, reload] = useLoad((s) => getCapabilities(s), "capabilities");
  const [tools, reloadTools] = useLoad((s) => getToolPolicy(s), "tool-policy");
  return (
    <>
      {tools.kind === "loading" ? (
        <Loading what="Assistant tools" />
      ) : tools.kind === "forbidden" ? (
        <Forbidden />
      ) : tools.kind === "error" ? (
        <Problem message={tools.message} onRetry={reloadTools} />
      ) : (
        <AssistantTools policy={tools.value} onSave={setToolPolicy} />
      )}
      {state.kind === "loading" ? (
        <Loading what="Assistant capabilities" />
      ) : state.kind === "forbidden" ? null : state.kind === "error" ? (
        <Problem message={state.message} onRetry={reload} />
      ) : (
        <CapabilitiesTable inv={state.value} />
      )}
    </>
  );
}

/**
 * Admin → Assistant tools: which tools every account's assistant has. Offered tools are checkboxes; the rest are
 * listed, locked, with the reason. Saving re-checks every account so the change takes effect.
 */
export function AssistantTools({ policy, onSave }: { policy: ToolPolicy; onSave: (allowed: string[]) => Promise<AdminResult<ToolPolicy>> }) {
  const [saved, setSaved] = useState(policy);
  const onNames = (p: ToolPolicy) => p.tools.filter((t) => t.state === "on" && t.reason === "").map((t) => t.name);
  const [chosen, setChosen] = useState(() => new Set(onNames(policy)));
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const offered = saved.tools.filter((t) => t.reason === "");
  const locked = saved.tools.filter((t) => t.reason !== "");
  const before = onNames(saved);
  const changed = before.length !== chosen.size || before.some((n) => !chosen.has(n));
  const toggle = (name: string) => {
    const next = new Set(chosen);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    setChosen(next);
    setStatus("");
  };
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const r = await onSave(offered.filter((t) => chosen.has(t.name)).map((t) => t.name));
    setBusy(false);
    if (!r.ok) {
      setError(describeAdminCode(r.code, r.status));
      return;
    }
    setSaved(r.value);
    setChosen(new Set(onNames(r.value)));
    const n = r.value.reverifying ?? 0;
    setStatus(`Saved. ${n === 1 ? "1 account is" : `${n} accounts are`} being re-checked; each one's chat pauses for about a minute while it is.`);
  };
  return (
    <section aria-labelledby="tools-h">
      <h1 id="tools-h">Assistant tools</h1>
      <p className="label">
        What every account's assistant can use. {saved.custom ? "Set here by an admin." : "This is the server's default until you save a choice."}
      </p>
      <form onSubmit={save} aria-label="Assistant tools">
        <ul className="tool-list">
          {offered.map((t) => (
            <li key={t.name}>
              <input type="checkbox" id={`tool-${t.name}`} checked={chosen.has(t.name)} disabled={busy} onChange={() => toggle(t.name)} />
              <label htmlFor={`tool-${t.name}`}>
                <strong>{t.label}</strong>
                {t.description && <small>{t.description}</small>}
              </label>
            </li>
          ))}
        </ul>
        {error && (
          <p className="notice" role="alert">
            {error}
          </p>
        )}
        <p className="tool-actions">
          <button type="submit" disabled={busy || !changed || chosen.size === 0}>
            {busy ? "Saving…" : "Save tools"}
          </button>
          <span role="status" className="label">
            {chosen.size === 0 ? "Choose at least one tool." : changed ? "Not saved yet." : status}
          </span>
        </p>
      </form>
      <details className="tool-locked">
        <summary>Not available to accounts ({locked.length})</summary>
        <ul>
          {locked.map((t) => (
            <li key={t.name}>
              <code>{t.name}</code>
              {t.state === "on" && <span className="badge bad">On by the server default</span>} {t.reason}
            </li>
          ))}
        </ul>
      </details>
    </section>
  );
}

// ---- provider account usage (#146) ---------------------------------------------

const rupiah = new Intl.NumberFormat("id-ID", { style: "currency", currency: "IDR", maximumFractionDigits: 0 });
const rupiahCents = new Intl.NumberFormat("id-ID", { style: "currency", currency: "IDR", minimumFractionDigits: 0, maximumFractionDigits: 2 });
/** Kenari reports balance and spend in micro-Rupiah (Rp 1 = 1,000,000); a request costs fractions of a Rupiah, so two decimals are kept. */
export const microRp = (n: number): string => rupiahCents.format(n / 1_000_000);
export const wholeRp = (n: number): string => rupiah.format(n);
const day = (s: number) => new Date(s * 1000).toISOString().slice(0, 10);

function SectionNote({ s, what }: { s: UsageSection<unknown> | null; what: string }) {
  if (!s) return <p className="label">{what}: not reported.</p>;
  if (s.state === "REFUSED")
    return (
      <p className="label" role="status">
        {what}: Kenari refused this key{s.code ? ` (${s.code})` : ""}. Balance and quota need an unrestricted key of the account. No figure is shown.
      </p>
    );
  return (
    <p className="label" role="status">
      {what}: Kenari did not answer. No figure is shown.
    </p>
  );
}

function UsageTotals({ u }: { u: KenariUsage }) {
  return (
    <div className="table-wrap">
      <table className="admin-table usage-table">
        <caption className="label">
          Whole provider account, {day(u.start)} to {day(u.end)} (UTC)
        </caption>
        <thead>
          <tr>
            <th scope="col">Model</th>
            <th scope="col">Billing</th>
            <th scope="col">Requests</th>
            <th scope="col">Input tokens</th>
            <th scope="col">Output tokens</th>
            <th scope="col">Charged to balance</th>
          </tr>
        </thead>
        <tbody>
          {u.rows.map((r) => (
            <tr key={`${r.model}/${r.billingKind}`}>
              <th scope="row">{r.model}</th>
              <td>{r.billingKind}</td>
              <td>{grouped(r.requests)}</td>
              <td>{grouped(r.promptTokens)}</td>
              <td>{grouped(r.completionTokens)}</td>
              <td>{microRp(r.costMicroIdr)}</td>
            </tr>
          ))}
          <tr>
            <th scope="row">Total</th>
            <td />
            <td>{grouped(u.total.requests)}</td>
            <td>{grouped(u.total.promptTokens)}</td>
            <td>{grouped(u.total.completionTokens)}</td>
            <td>{microRp(u.total.costMicroIdr)}</td>
          </tr>
        </tbody>
      </table>
    </div>
  );
}

export function UsagePanel({ v, onRefresh, busy }: { v: KenariUsageView; onRefresh: () => void; busy: boolean }) {
  const refresh = (
    <button type="button" className="quiet" onClick={onRefresh} disabled={busy}>
      {busy ? "Refreshing…" : "Refresh"}
    </button>
  );
  return (
    <section aria-labelledby="usage-h">
      <h1 id="usage-h">Provider usage</h1>
      <p className="label">
        Shared provider account (kenari.id), read with the server's own key. One account serves every Antares user, so none of this is a
        per-user or per-chat figure. Token counts shown in chats are local estimates from the assistant and are a different number.
      </p>
      {v.state === "NOT_CONFIGURED" && <p role="status">Not configured: the operator has not configured a provider key for Antares. No figure is shown.</p>}
      {v.state === "UNAVAILABLE" && (
        <p role="alert">
          Kenari could not be read and no recent answer is held. No figure is shown. {refresh}
        </p>
      )}
      {v.state === "OK" && (
        <>
          <p className="label">
            Read from Kenari {v.fetchedAt ? when(v.fetchedAt) : ""}. {refresh}
          </p>
          {v.stale && (
            <p role="alert">Stale: the latest attempt to read Kenari failed, so these are the last figures it gave. Refresh to try again.</p>
          )}
          <h2>Balance</h2>
          {v.balance?.state === "OK" && v.balance.data ? (
            <dl className="stat-tiles">
              <div className="stat-tile lead">
                <dt>Available to spend</dt>
                <dd>{microRp(v.balance.data.availableMicroIdr)}</dd>
              </div>
              <div className="stat-tile">
                <dt>Balance</dt>
                <dd>{microRp(v.balance.data.balanceMicroIdr)}</dd>
              </div>
              <div className="stat-tile">
                <dt>Held by running requests</dt>
                <dd>{microRp(v.balance.data.reservedMicroIdr)}</dd>
              </div>
            </dl>
          ) : (
            <SectionNote s={v.balance} what="Balance" />
          )}
          <h2>Plan quota</h2>
          {v.quota?.state === "OK" && v.quota.data ? (
            <>
              {v.quota.data.plan ? (
                <section className="quota-meter quota-admin" aria-label={`Plan ${v.quota.data.plan.name} quota`}>
                  <p className="quota-title">Plan {v.quota.data.plan.name}, shared by every account</p>
                  {v.quota.data.plan.windows.map((w) => (
                    <QuotaBar
                      key={w.key}
                      label={WINDOW_LABELS[w.key] ?? w.key}
                      frac={w.usedFrac}
                      resetsAt={w.resetsAt}
                      detail={`${wholeRp(w.usedRp)} of ${wholeRp(w.budgetRp)}, ${wholeRp(w.remainingRp)} left`}
                    />
                  ))}
                </section>
              ) : (
                <p className="label">The account has no active plan.</p>
              )}
              {v.quota.data.coupon && (
                <p>
                  Coupon {v.quota.data.coupon.name}: used {wholeRp(v.quota.data.coupon.usedRp)},{" "}
                  {v.quota.data.coupon.remainingRp === null ? "no spending limit" : `${wholeRp(v.quota.data.coupon.remainingRp)} left`}, expires {when(v.quota.data.coupon.expiresAt)}.
                </p>
              )}
            </>
          ) : (
            <SectionNote s={v.quota} what="Quota" />
          )}
          <h2>Usage, last 30 days</h2>
          {v.usage?.state === "OK" && v.usage.data ? <UsageTotals u={v.usage.data} /> : <SectionNote s={v.usage} what="Usage" />}
        </>
      )}
    </section>
  );
}

export function UsageScreen() {
  const [refreshes, setRefreshes] = useState(0);
  const [state, reload] = useLoad((s) => getKenariUsage(refreshes > 0, s), `usage-${refreshes}`);
  const last = useRef<KenariUsageView | null>(null);
  if (state.kind === "ready") last.current = state.value;
  if (state.kind === "loading" && !last.current) return <Loading what="Provider usage" />;
  if (state.kind === "forbidden") return <Forbidden />;
  if (state.kind === "error") return <Problem message={state.message} onRetry={reload} />;
  const v = state.kind === "ready" ? state.value : last.current!;
  return <UsagePanel v={v} busy={state.kind === "loading"} onRefresh={() => setRefreshes((n) => n + 1)} />;
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
      return <UserScreen key={route.id} id={route.id} />;
    case "audit":
      return <AuditScreen />;
    case "capabilities":
      return <CapabilitiesScreen />;
    case "usage":
      return <UsageScreen />;
    case "unknown":
      return <NotFound />;
  }
}

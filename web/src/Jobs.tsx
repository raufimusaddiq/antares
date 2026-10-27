import { useEffect, useState, type FormEvent } from "react";
import { createJob, deleteJob, describeJobCode, jobAction, listJobs, type Job } from "./chatApi";

// Scheduled tasks (#105, PRD §12): the account's own jobs in its own workspace. Read and changed only while the
// panel is open. A task's results stay in the workspace: the assistant offers no route to read them back, so the panel
// shows when a task last ran and how that went, not what it produced.

/** A time in UTC, as the panel says: "2026-10-27 09:00 UTC". */
export const when = (s: string | null) => {
  const d = s ? new Date(s) : null;
  return d && !Number.isNaN(d.getTime()) ? `${d.toISOString().slice(0, 16).replace("T", " ")} UTC` : "—";
};

export function JobsPanel() {
  const [open, setOpen] = useState(false);
  const [jobs, setJobs] = useState<Job[] | null>(null);
  const [max, setMax] = useState(10);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState({ name: "", schedule: "", prompt: "" });

  const reload = async () => {
    const r = await listJobs();
    if (r.ok) {
      setJobs(r.value.jobs);
      setMax(r.value.max);
      setError("");
    } else {
      setJobs(null);
      setError(describeJobCode(r.code));
    }
  };
  useEffect(() => {
    if (open) void reload();
  }, [open]);

  const act = async (fn: () => Promise<{ ok: boolean; code?: string }>) => {
    setBusy(true);
    const r = await fn();
    setBusy(false);
    if (!r.ok) {
      setError(describeJobCode((r as { code: string }).code));
      return false;
    }
    await reload();
    return true;
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    void act(() => createJob(draft)).then((ok) => ok && setDraft({ name: "", schedule: "", prompt: "" }));
  };

  return (
    <details className="jobs-panel" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>Scheduled tasks</summary>
      {open && (
        <>
          <p className="label">Tasks run in your own workspace with the same tools as your chats, at most once an hour. Times are UTC. Results stay in the workspace; this list shows when each task last ran.</p>
          {error && <p role="alert">{error}</p>}
          {jobs === null ? (
            !error && <p className="label">Loading…</p>
          ) : jobs.length === 0 ? (
            <p className="label">No scheduled tasks.</p>
          ) : (
            <ul className="jobs">
              {jobs.map((j) => (
                <li key={j.id}>
                  <strong>{j.name}</strong> · {j.schedule} · {j.paused ? "paused" : j.state}
                  <br />
                  <span className="label">
                    Next {when(j.nextRunAt)} · last ran {when(j.lastRunAt)}
                    {j.lastStatus ? ` (${j.lastStatus})` : ""}
                  </span>
                  <div className="actions">
                    <button type="button" className="quiet" disabled={busy} onClick={() => void act(() => jobAction(j.id, j.paused ? "resume" : "pause"))}>
                      {j.paused ? "Resume" : "Pause"} {j.name}
                    </button>
                    <button type="button" className="quiet" disabled={busy} onClick={() => void act(() => jobAction(j.id, "run"))}>
                      Run {j.name} now
                    </button>
                    <button type="button" className="quiet" disabled={busy} onClick={() => window.confirm(`Delete the task "${j.name}"?`) && void act(() => deleteJob(j.id))}>
                      Delete {j.name}
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
          {jobs !== null && jobs.length < max && (
            <form onSubmit={submit}>
              <label>
                Name <input value={draft.name} maxLength={100} required onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
              </label>{" "}
              <label>
                Schedule <input value={draft.schedule} maxLength={100} required placeholder="every 2h, or 0 9 * * *" onChange={(e) => setDraft({ ...draft, schedule: e.target.value })} />
              </label>{" "}
              <label>
                Instructions <textarea value={draft.prompt} maxLength={4000} required onChange={(e) => setDraft({ ...draft, prompt: e.target.value })} />
              </label>{" "}
              <button type="submit" disabled={busy}>
                Add task
              </button>
            </form>
          )}
        </>
      )}
    </details>
  );
}

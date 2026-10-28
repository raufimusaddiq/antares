import { Panel } from "./Panel";
import { useEffect, useRef, useState } from "react";
import { describeCode, getCapabilities, getPersonality, putPersonality, type Inventory } from "./chatApi";

// Memory & Skills (#111, PRD §26.1 Memory/knowledge and Persona, KNOW-001..007). Everything here is the caller's own
// workspace: its installed skills (read-only), its memory (the assistant offers no route to read or edit it, so this
// says so instead of guessing), and the caller's own additional instructions, applied from the next message.
// Read only while the panel is open.

export function MemorySkillsPanel({ page = false }: { page?: boolean } = {}) {
  const [open, setOpen] = useState(page);
  const [inv, setInv] = useState<Inventory | null>(null);
  const [invError, setInvError] = useState("");
  const [query, setQuery] = useState("");
  const [text, setText] = useState("");
  const [saved, setSaved] = useState<string | null>(null);
  const [max, setMax] = useState(2000);
  const [status, setStatus] = useState("");
  const [saving, setSaving] = useState(false);
  const seq = useRef(0); // orders reads and writes of the instructions: only the newest one's answer is applied
  const savingRef = useRef(false);

  useEffect(() => {
    if (!open) return;
    let live = true;
    setInv(null);
    setInvError(""); // a reopen reads again: an earlier failure is not the answer any more
    void getCapabilities().then((r) => {
      if (!live) return;
      if (r.ok) setInv(r.value);
      else setInvError(describeCode(r.code));
    });
    // A save still pending owns the box: no read replaces the edit it is sending, and if it fails the edit stays to retry.
    if (savingRef.current) {
      return () => {
        live = false;
      };
    }
    // Until this read succeeds there is no baseline, so Save stays off: stale text cannot overwrite another session.
    setSaved(null);
    const mine = ++seq.current;
    void getPersonality().then((r) => {
      if (!live || mine !== seq.current) return; // a later read or a save since then has the newer answer
      if (r.ok) {
        setText(r.value.text);
        setSaved(r.value.text);
        setMax(r.value.max);
      } else setStatus(describeCode(r.code));
    });
    return () => {
      live = false;
    };
  }, [open]);

  const save = async () => {
    setSaving(true); // the editor is read-only until the server answers, so nothing typed meanwhile is overwritten
    savingRef.current = true;
    setStatus("Saving…");
    ++seq.current; // any read already in flight is older than this write: its answer is dropped
    const r = await putPersonality(text);
    ++seq.current; // and so is any read that started while the write was pending
    savingRef.current = false;
    setSaving(false);
    if (r.ok) {
      setText(r.value.text);
      setSaved(r.value.text); // what the server stored, not what was typed
      setStatus(r.value.text ? "Saved. It applies from your next message." : "Cleared. Your next message carries no extra instructions.");
    } else setStatus(`Not saved: ${describeCode(r.code)}`);
  };

  const skillsRow = inv?.capabilities.find((c) => c.id === "skills");
  const q = query.trim().toLowerCase();
  const skills = (inv?.skills ?? []).filter((s) => !q || `${s.name} ${s.category} ${s.description}`.toLowerCase().includes(q));

  return (
    <Panel page={page} className="memory-panel" title="Memory & Skills" onToggle={setOpen}>
      {open && (
        <>
          <h3>Memory</h3>
          <p className="label">
            The assistant keeps its own long-term memory in your workspace. Viewing, editing or forgetting a stored fact here is not available: the assistant offers no route for it. To have something removed, ask the operator.
          </p>

          <h3>Skills</h3>
          {invError ? (
            <p role="alert">{invError}</p>
          ) : !inv ? (
            <p className="label">Loading…</p>
          ) : !skillsRow?.available ? (
            <p className="label">No skills are available{skillsRow?.reason ? `: ${skillsRow.reason}` : "."}</p>
          ) : (
            <>
              <label>
                Search skills <input type="search" value={query} onChange={(e) => setQuery(e.target.value)} />
              </label>
              {skills.length === 0 ? (
                <p className="label">No skill matches.</p>
              ) : (
                <ul className="skills">
                  {skills.map((s) => (
                    <li key={s.name}>
                      <strong>{s.name}</strong>
                      {s.category && <span className="label"> · {s.category}</span>}
                      {s.description && <p>{s.description}</p>}
                    </li>
                  ))}
                </ul>
              )}
              <p className="label">Read-only: skills are installed and removed by the operator.</p>
            </>
          )}

          <h3>Your instructions</h3>
          <p className="label">Added to each of your messages as preferences. The assistant's base instructions are set by the operator, are not shown here and always come first.</p>
          <label>
            Instructions <textarea value={text} maxLength={max} readOnly={saving || saved === null} aria-busy={saving || saved === null} onChange={(e) => setText(e.target.value)} />
          </label>
          <button type="button" disabled={saving || saved === null || text.trim() === saved} onClick={() => void save()}>
            Save instructions
          </button>
          {status && <p role="status">{status}</p>}
        </>
      )}
    </Panel>
  );
}

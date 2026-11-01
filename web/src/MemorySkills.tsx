import { Panel } from "./Panel";
import { useEffect, useRef, useState } from "react";
import { describeCode, getCapabilities, getMemory, getPersonality, putPersonality, type Inventory, type Memory } from "./chatApi";

// Memory & Skills (#111, PRD §26.1 Memory/knowledge and Persona, KNOW-001..007). Everything here is the caller's own
// workspace: what the assistant remembers (read only, #248: read from the workspace by the provisioning helper, as
// the assistant offers no memory route), its installed skills (read-only), and the caller's own additional instructions,
// applied from the next message.
// Read only while the panel is open.

export function MemorySkillsPanel({ page = false }: { page?: boolean } = {}) {
  const [open, setOpen] = useState(page);
  const [inv, setInv] = useState<Inventory | null>(null);
  const [invError, setInvError] = useState("");
  const [memory, setMemory] = useState<Memory | null>(null);
  const [memoryError, setMemoryError] = useState("");
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
    setMemory(null);
    setMemoryError("");
    void getMemory().then((r) => {
      if (!live) return;
      if (r.ok) setMemory(r.value);
      else setMemoryError(describeCode(r.code));
    });
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
          {memoryError ? (
            <p role="alert">{memoryError}</p>
          ) : !memory ? (
            <p className="label">Loading…</p>
          ) : (
            <>
              <MemoryList title="About you" entries={memory.user} empty="Nothing remembered about you yet." />
              <MemoryList title="The assistant's notes" entries={memory.memory} empty="No notes yet." />
            </>
          )}
          <p className="label">Read-only. The assistant adds and updates these itself as you chat. To change or remove one, ask it in a chat.</p>

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

function MemoryList({ title, entries, empty }: { title: string; entries: string[]; empty: string }) {
  return (
    <>
      <h4>{title}</h4>
      {entries.length === 0 ? (
        <p className="label">{empty}</p>
      ) : (
        <ul className="memory-entries">
          {entries.map((e, i) => (
            <li key={i}>{e}</li>
          ))}
        </ul>
      )}
    </>
  );
}

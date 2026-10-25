import { useState, type FormEvent } from "react";
import { addTemplate, draftBytes, MAX_TEMPLATE_BYTES, removeTemplate, saveTemplate, type OwnTemplate } from "./draftApi";

// Your own slash templates: add, edit and delete. A template is text the composer inserts for you to
// edit; choosing one runs nothing. Built-in ones are not listed here and cannot be changed.

const WORDS = {
  taken: "You already have a template with that name.",
  invalid: "The server did not accept that name or text.",
  gone: "That template no longer exists. It may have been deleted on another device.",
  failed: "Could not save the template.",
} as const;

export function Templates({ own, onChange }: { own: OwnTemplate[]; onChange: (l: OwnTemplate[]) => void }) {
  const [editing, setEditing] = useState<string | null>(null); // id of the template in the form; null = adding
  const [name, setName] = useState("");
  const [body, setBody] = useState("");
  const [msg, setMsg] = useState("");
  const reset = () => {
    setEditing(null);
    setName("");
    setBody("");
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const bytes = draftBytes(body);
    if (bytes > MAX_TEMPLATE_BYTES) {
      // The bound is bytes (a CJK character is 3), and the box counts characters: say the real reason, send nothing.
      return setMsg(`The text is ${bytes.toLocaleString("en-US")} bytes; the limit is ${MAX_TEMPLATE_BYTES.toLocaleString("en-US")} bytes. Shorten it.`);
    }
    if (editing === null) {
      const r = await addTemplate(name, body);
      if (!r.ok) return setMsg(r.message ?? WORDS[r.reason]);
      onChange([...own, r.value]);
    } else {
      const r = await saveTemplate(editing, name, body);
      if (!r.ok) {
        setMsg(r.message ?? WORDS[r.reason]);
        if (r.reason === "gone") {
          onChange(own.filter((x) => x.id !== editing));
          reset();
        }
        return; // taken/invalid/failed: the form keeps what was typed
      }
      onChange(own.map((x) => (x.id === editing ? r.value : x)));
    }
    setMsg("");
    reset();
  };
  const edit = (t: OwnTemplate) => {
    setEditing(t.id);
    setName(t.name);
    setBody(t.body);
    setMsg("");
  };
  const remove = async (t: OwnTemplate) => {
    if (await removeTemplate(t.id)) {
      onChange(own.filter((x) => x.id !== t.id));
      if (editing === t.id) reset();
    } else setMsg("Could not delete the template.");
  };
  return (
    <details className="template-manager">
      <summary data-icon="bookmark">My templates{own.length > 0 ? ` (${own.length})` : ""}</summary>
      <p className="label">Type / in the message box to insert one. Nothing is run when you pick it.</p>
      <ul>
        {own.map((t) => (
          <li key={t.id}>
            <code>/{t.name}</code>
            <button type="button" className="quiet" aria-label={`Edit template ${t.name}`} onClick={() => edit(t)}>
              Edit
            </button>
            <button type="button" className="quiet" aria-label={`Delete template ${t.name}`} onClick={() => void remove(t)}>
              Delete
            </button>
          </li>
        ))}
      </ul>
      <form onSubmit={(e) => void submit(e)}>
        <label>
          Name
          <input value={name} maxLength={32} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          Text
          <textarea value={body} rows={3} maxLength={8192} onChange={(e) => setBody(e.target.value)} />
        </label>
        <button type="submit" disabled={name.trim() === "" || body === ""}>
          {editing === null ? "Add template" : "Save changes"}
        </button>
        {editing !== null && (
          <button type="button" className="quiet" onClick={() => { reset(); setMsg(""); }}>
            Cancel
          </button>
        )}
      </form>
      {msg && <p role="alert">{msg}</p>}
    </details>
  );
}

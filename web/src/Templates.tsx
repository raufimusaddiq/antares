import { useState, type FormEvent } from "react";
import { addTemplate, removeTemplate, type OwnTemplate } from "./draftApi";

// Your own slash templates. A template is text the composer inserts for you to edit; choosing one
// runs nothing. Built-in ones are not listed here and cannot be removed.

export function Templates({ own, onChange }: { own: OwnTemplate[]; onChange: (l: OwnTemplate[]) => void }) {
  const [name, setName] = useState("");
  const [body, setBody] = useState("");
  const [msg, setMsg] = useState("");
  const add = async (e: FormEvent) => {
    e.preventDefault();
    const r = await addTemplate(name, body);
    if (!r.ok) {
      setMsg(r.reason === "taken" ? "You already have a template with that name." : r.reason === "invalid" ? "A name is 1-32 letters, digits, - or _; the text is required." : "Could not save the template.");
      return;
    }
    setMsg("");
    setName("");
    setBody("");
    onChange([...own, r.value]);
  };
  const remove = async (t: OwnTemplate) => {
    if (await removeTemplate(t.id)) onChange(own.filter((x) => x.id !== t.id));
    else setMsg("Could not delete the template.");
  };
  return (
    <details className="template-manager">
      <summary>My templates{own.length > 0 ? ` (${own.length})` : ""}</summary>
      <p className="label">Type / in the message box to insert one. Nothing is run when you pick it.</p>
      <ul>
        {own.map((t) => (
          <li key={t.id}>
            <code>/{t.name}</code>
            <button type="button" className="quiet" aria-label={`Delete template ${t.name}`} onClick={() => void remove(t)}>
              Delete
            </button>
          </li>
        ))}
      </ul>
      <form onSubmit={(e) => void add(e)}>
        <label>
          Name
          <input value={name} maxLength={32} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          Text
          <textarea value={body} rows={3} maxLength={8192} onChange={(e) => setBody(e.target.value)} />
        </label>
        <button type="submit" disabled={name.trim() === "" || body === ""}>
          Add template
        </button>
      </form>
      {msg && <p role="alert">{msg}</p>}
    </details>
  );
}

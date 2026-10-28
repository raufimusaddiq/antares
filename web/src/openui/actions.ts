import { MAX_REPLY } from "./reply";

// The single choke point for everything a model-generated component can do
// (PRD §7.4). It imports nothing from the OpenUI runtime, but only Block.tsx imports it,
// so it ships in the on-demand Block-*.js chunk, not the main bundle.
//
// An action is a name plus a payload. Both come from a block that the model
// wrote, so neither is trusted: the name must be an own key of ACTIONS (a
// prototype name such as "constructor" is not an action) and the payload is
// rebuilt by that action's sanitiser before the handler sees it. The only action
// is "reply": it hands plain text to the composer through the sink the app
// provides (ReplyContext) and sends nothing. There is no Go route behind it
// because it never leaves the browser; an action that needs the server must be
// added here together with its secured route (same session, approval, ownership
// and CSRF guards as any request), never by calling fetch from a component.
//
// Approving a run and answering the assistant's question (issue #104) are deliberately NOT actions: a
// component the model wrote must never be able to approve the model's own tool call. Those are buttons
// in the app's own RunPrompts card, backed by POST /api/runs/{id}/approve and /clarify. A test pins
// ALLOWED_ACTIONS so adding one here is a visible decision.
//
// Native OpenUI (the chat component library) emits its two built-in actions here: "continue_conversation"
// (@ToAssistant, a follow-up button or a submitted form) becomes text in the composer, never a sent message;
// "open_url" (@OpenUrl) opens an http(s) address in a new tab without a referrer, after the reader confirms it.

export type Verdict = { ok: true } | { ok: false; reason: string };
export type Sinks = { reply?: ((text: string) => void) | null; open?: ((url: string) => void) | null };

// C0/C1 controls except tab and newline, and the bidi override/isolate characters.
// eslint-disable-next-line no-control-regex
const UNSAFE = /[\u0000-\u0008\u000B-\u001F\u007F-\u009F\u202A-\u202E\u2066-\u2069]/g;

// Sanitisation point for model-supplied action text: the only function that turns
// an untrusted payload into the string a handler receives.
export function cleanReply(payload: unknown): string | null {
  if (typeof payload !== "string") return null;
  const t = payload.replace(UNSAFE, "").trim();
  return t === "" || t.length > MAX_REPLY ? null : t;
}

const ACTIONS: Record<string, { run: (payload: unknown, sinks: Sinks) => Verdict }> = {
  reply: {
    run(payload, sinks) {
      const t = cleanReply(payload);
      if (t === null) return { ok: false, reason: "invalid payload" };
      if (!sinks.reply) return { ok: false, reason: "no composer" };
      sinks.reply(t);
      return { ok: true };
    },
  },
};

// An OpenUI action event: { type, params, humanFriendlyMessage, formState?, formName? }.
const obj = (v: unknown): Record<string, unknown> | null => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null);

/** The follow-up text of an OpenUI @ToAssistant event: its message, then any form values as JSON. */
export function followUpText(event: unknown): string | null {
  const e = obj(event);
  if (!e) return null;
  const params = obj(e.params) ?? {};
  const message = typeof params.message === "string" ? params.message : typeof e.humanFriendlyMessage === "string" ? e.humanFriendlyMessage : "";
  const extra = obj(e.formState) ?? (params.context !== undefined ? { context: params.context } : null);
  const json = extra && Object.keys(extra).length > 0 ? "\n\n```json\n" + JSON.stringify(extra, null, 2) + "\n```" : "";
  return cleanReply(message + json);
}

/** An address @OpenUrl may open: absolute http(s) only, with no user name or password in it. */
export function safeUrl(event: unknown): string | null {
  const raw = obj(obj(event)?.params)?.url;
  if (typeof raw !== "string" || raw.length > 2048) return null;
  try {
    const u = new URL(raw);
    return (u.protocol === "https:" || u.protocol === "http:") && !u.username && !u.password ? u.href : null;
  } catch {
    return null;
  }
}

ACTIONS.continue_conversation = {
  run(event, sinks) {
    const t = followUpText(event);
    if (t === null) return { ok: false, reason: "invalid payload" };
    if (!sinks.reply) return { ok: false, reason: "no composer" };
    sinks.reply(t);
    return { ok: true };
  },
};
ACTIONS.open_url = {
  run(event, sinks) {
    const url = safeUrl(event);
    if (url === null) return { ok: false, reason: "invalid url" };
    if (!sinks.open) return { ok: false, reason: "no opener" };
    sinks.open(url);
    return { ok: true };
  },
};

export const ALLOWED_ACTIONS: readonly string[] = Object.keys(ACTIONS);

export function runAction(name: unknown, payload: unknown, sinks: Sinks): Verdict {
  if (typeof name !== "string" || !Object.hasOwn(ACTIONS, name)) return { ok: false, reason: "action not allowed" };
  return ACTIONS[name].run(payload, sinks);
}

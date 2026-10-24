import { MAX_REPLY } from "./reply";

// The single choke point for everything a model-generated component can do
// (PRD §7.4). No import of the OpenUI runtime, so it is in the main bundle.
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

export type Verdict = { ok: true } | { ok: false; reason: string };
export type Sinks = { reply?: ((text: string) => void) | null };

// C0/C1 controls except tab and newline, and the bidi override/isolate characters.
// eslint-disable-next-line no-control-regex
const UNSAFE = /[\u0000-\u0008\u000B-\u001F\u007F-\u009F‪-‮⁦-⁩]/g;

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

export const ALLOWED_ACTIONS: readonly string[] = Object.keys(ACTIONS);

export function runAction(name: unknown, payload: unknown, sinks: Sinks): Verdict {
  if (typeof name !== "string" || !Object.hasOwn(ACTIONS, name)) return { ok: false, reason: "action not allowed" };
  return ACTIONS[name].run(payload, sinks);
}

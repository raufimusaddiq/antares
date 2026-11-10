// The administrator's Models screen (specs/004-admin-full-control): every model with a switch, the default per purpose, usage per model.
// The server decides what each purpose runs and whether a saved default still works; nothing here guesses either.

import { call, type AdminResult } from "./adminApi";

export type Purpose = "chat" | "summary" | "blog_assistant" | "image" | "video";
export const PURPOSES: readonly Purpose[] = ["chat", "summary", "blog_assistant", "image", "video"];

export const PURPOSE_COPY: Record<Purpose, { label: string; hint: string }> = {
  chat: { label: "Chat", hint: "Every new chat, and chats where the person has not chosen a model." },
  summary: { label: "Summaries and titles", hint: "Sidebar titles and shared-picture summaries." },
  blog_assistant: { label: "Blog assistant", hint: "The post check an author runs before publishing." },
  image: { label: "Images", hint: "Pictures the assistant makes." },
  video: { label: "Videos", hint: "Videos the assistant makes. Billed by the second." },
};

export interface ModelRow {
  id: string;
  name: string;
  on: boolean;
  purposes: Purpose[];
  defaultFor: Purpose[];
  /** Calls and tokens over 30 days, per model and never per person; null when none was recorded. */
  usage: { calls: number; tokens: number } | null;
}

export interface PurposeState {
  /** "" is the service default. */
  current: string;
  effective: string;
  /** Why the saved default is not in use: not_listed, sunset or off. */
  warning: string;
}

export interface ModelsView {
  catalogue: "ok" | "unavailable";
  models: ModelRow[];
  purposes: Record<Purpose, PurposeState>;
}

const obj = (v: unknown): Record<string, unknown> | null => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null);
const str = (v: unknown): string => (typeof v === "string" ? v : "");
const purposesOf = (v: unknown): Purpose[] => (Array.isArray(v) ? v.filter((p): p is Purpose => PURPOSES.includes(p as Purpose)) : []);

export function parseModels(v: unknown): ModelsView | null {
  const o = obj(v);
  const ps = obj(o?.purposes);
  if (!o || !ps || !Array.isArray(o.models) || (o.catalogue !== "ok" && o.catalogue !== "unavailable")) return null;
  const models: ModelRow[] = [];
  for (const m of o.models) {
    const x = obj(m);
    if (!x || !str(x.id)) return null;
    const u = obj(x.usage);
    models.push({
      id: str(x.id),
      name: str(x.name) || str(x.id),
      on: x.on === true,
      purposes: purposesOf(x.purposes),
      defaultFor: purposesOf(x.defaultFor),
      usage: u && typeof u.calls === "number" && typeof u.tokens === "number" ? { calls: u.calls, tokens: u.tokens } : null,
    });
  }
  const purposes = {} as Record<Purpose, PurposeState>;
  for (const p of PURPOSES) {
    const x = obj(ps[p]);
    if (!x) return null;
    purposes[p] = { current: str(x.current), effective: str(x.effective), warning: str(x.warning) };
  }
  return { catalogue: o.catalogue, models, purposes };
}

export const getModels = (signal?: AbortSignal) => call("GET", "/api/admin/models", parseModels, undefined, signal);

/** Switch models off or on: the list of models that are off. A model that is a purpose's default is refused (MODEL_IS_DEFAULT, with the purposes). */
export const setBlocked = (blocked: string[]): Promise<AdminResult<string[]>> =>
  call("PUT", "/api/admin/model-policy", (v) => {
    const b = obj(v)?.blocked;
    return Array.isArray(b) ? b.filter((x): x is string => typeof x === "string") : null;
  }, { blocked });

/** Save defaults for chat, summaries and titles, and the blog assistant ("" = the service default); a purpose left out is unchanged. */
export const setDefaults = (d: Partial<Record<"chat" | "summary" | "blog_assistant", string>>) => call("PUT", "/api/admin/model-defaults", parseModels, d);
/** Save the image and video defaults (both are always sent, as the server requires; "" = the plugin's own). */
export const setMedia = (image: string, video: string) => call("PUT", "/api/admin/media-models", () => true as const, { image, video });

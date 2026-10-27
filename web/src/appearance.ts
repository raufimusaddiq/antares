// Per-device appearance (PRD §26.1 Appearance, issue #113). Kept in this browser only (it describes the device, not the
// account) and applied as attributes on <html> before the first paint, so styles.css can follow them. "system" theme
// follows the OS; a reduced-motion OS setting is always honoured (styles.css), whatever is chosen here.

export type Theme = "system" | "light" | "dark";
export type TextSize = "normal" | "large" | "larger";
export type SidebarWidth = "narrow" | "normal" | "wide";

export interface Appearance {
  theme: Theme;
  text: TextSize;
  /** Hide the conversation list on a wide screen so the conversation takes the room. */
  focus: boolean;
  sidebar: SidebarWidth;
}

export const DEFAULT_APPEARANCE: Appearance = { theme: "system", text: "normal", focus: false, sidebar: "normal" };
export const APPEARANCE_KEY = "antares:appearance:v1";

const pick = <T extends string>(v: unknown, allowed: readonly T[], fallback: T): T => (allowed.includes(v as T) ? (v as T) : fallback);

export function loadAppearance(): Appearance {
  try {
    const raw = JSON.parse(localStorage.getItem(APPEARANCE_KEY) ?? "{}") as Record<string, unknown>;
    return {
      theme: pick(raw.theme, ["system", "light", "dark"], DEFAULT_APPEARANCE.theme),
      text: pick(raw.text, ["normal", "large", "larger"], DEFAULT_APPEARANCE.text),
      focus: raw.focus === true,
      sidebar: pick(raw.sidebar, ["narrow", "normal", "wide"], DEFAULT_APPEARANCE.sidebar),
    };
  } catch {
    return { ...DEFAULT_APPEARANCE };
  }
}

export function saveAppearance(a: Appearance): void {
  try {
    localStorage.setItem(APPEARANCE_KEY, JSON.stringify(a));
  } catch {
    // Storage unavailable: the choice holds for this page only.
  }
}

/** Sets the attributes styles.css reads; the defaults are no attribute at all, so nothing changes until a choice is made. */
export function applyAppearance(a: Appearance, root: HTMLElement = document.documentElement): void {
  const set = (name: string, value: string | null) => (value === null ? root.removeAttribute(name) : root.setAttribute(name, value));
  set("data-theme", a.theme === "system" ? null : a.theme);
  set("data-text", a.text === "normal" ? null : a.text);
  set("data-focus", a.focus ? "" : null);
  set("data-sidebar", a.sidebar === "normal" ? null : a.sidebar);
}

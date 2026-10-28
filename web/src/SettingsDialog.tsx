import { useEffect, useRef, useState } from "react";
import { JobsPanel } from "./Jobs";
import { MemorySkillsPanel } from "./MemorySkills";
import { Notifications } from "./Notifications";
import { AppearancePanel, DiagnosticsPanel } from "./Settings";
import { UsageDashboard } from "./UsageDashboard";

export type SettingsTab = "general" | "memory" | "usage" | "jobs" | "diagnostics";

const TABS: { id: SettingsTab; label: string }[] = [
  { id: "general", label: "General" },
  { id: "memory", label: "Memory & skills" },
  { id: "usage", label: "Usage" },
  { id: "jobs", label: "Scheduled tasks" },
  { id: "diagnostics", label: "Diagnostics" },
];

/**
 * Everything about the account and this device, in one modal: a list of sections and the open section. Only the open
 * section is mounted, so each one loads when it is shown. Escape or the close button closes it; the caller puts focus
 * back where it was.
 */
export function SettingsDialog(props: { tab: SettingsTab; jobs: boolean; onClose: () => void; onSignOutEverywhere: () => void; signOutError?: string }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [tab, setTab] = useState<SettingsTab>(props.tab);
  const tabs = TABS.filter((t) => t.id !== "jobs" || props.jobs);
  useEffect(() => {
    const d = ref.current;
    if (!d || d.open) return;
    if (typeof d.showModal === "function") d.showModal();
    else d.setAttribute("open", ""); // no modal support: shown in place
  }, []);
  return (
    <dialog ref={ref} className="settings-dialog" aria-labelledby="settings-title" onClose={props.onClose} onCancel={props.onClose}>
      <header>
        <h2 id="settings-title">Settings</h2>
        <button type="button" className="quiet icon-only" data-icon="close" aria-label="Close settings" onClick={props.onClose} />
      </header>
      <div className="settings-body">
        <nav aria-label="Settings sections">
          {tabs.map((t) => (
            <button key={t.id} type="button" aria-current={tab === t.id ? "page" : undefined} onClick={() => setTab(t.id)}>
              {t.label}
            </button>
          ))}
        </nav>
        <section className="settings-page" aria-label={tabs.find((t) => t.id === tab)?.label}>
          {tab === "general" && (
            <>
              <h3>Appearance</h3>
              <AppearancePanel page />
              <h3>Notifications</h3>
              <Notifications page />
              <h3>Sessions</h3>
              <p className="label">Ends this account's sessions on every device; each one has to sign in again.</p>
              <button type="button" className="quiet" data-icon="logout" onClick={props.onSignOutEverywhere}>
                Sign out everywhere
              </button>
              {props.signOutError && <p role="alert">{props.signOutError}</p>}
            </>
          )}
          {tab === "memory" && <MemorySkillsPanel page />}
          {tab === "usage" && <UsageDashboard page />}
          {tab === "jobs" && props.jobs && <JobsPanel page />}
          {tab === "diagnostics" && <DiagnosticsPanel page />}
        </section>
      </div>
    </dialog>
  );
}

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
 * Everything about the account and this device, in one modal: a tab list and the open tab's page. Only the open page
 * is mounted, so each one loads when it is shown. Escape or the close button returns focus to the menu.
 */
export function SettingsDialog(props: { tab: SettingsTab; jobs: boolean; onClose: () => void; onSignOutEverywhere: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [tab, setTab] = useState<SettingsTab>(props.tab);
  const tabs = TABS.filter((t) => t.id !== "jobs" || props.jobs);
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) d.showModal?.();
  }, []);
  return (
    <dialog ref={ref} className="settings-dialog" aria-labelledby="settings-title" onClose={props.onClose} onCancel={props.onClose}>
      <header>
        <h2 id="settings-title">Settings</h2>
        <button type="button" className="quiet icon-only" data-icon="close" aria-label="Close settings" onClick={props.onClose} />
      </header>
      <div className="settings-body">
        <nav role="tablist" aria-label="Settings sections">
          {tabs.map((t) => (
            <button key={t.id} type="button" role="tab" aria-selected={tab === t.id} aria-controls="settings-page" onClick={() => setTab(t.id)}>
              {t.label}
            </button>
          ))}
        </nav>
        <div id="settings-page" role="tabpanel" aria-label={tabs.find((t) => t.id === tab)?.label}>
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
            </>
          )}
          {tab === "memory" && <MemorySkillsPanel page />}
          {tab === "usage" && <UsageDashboard page />}
          {tab === "jobs" && props.jobs && <JobsPanel page />}
          {tab === "diagnostics" && <DiagnosticsPanel page />}
        </div>
      </div>
    </dialog>
  );
}

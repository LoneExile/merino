// Extracted from the 1052-line SettingsSheet, where every tab's state lived in
// one scope and all five JSX trees were built on every render. Each tab now
// owns exactly the state it uses, so a reader can follow one without holding
// the other four.

import { useCallback, useEffect, useState } from "react";
import type { Client, Session } from "../client";
import { Sheet } from "../Sheet";
import type { ThemePref } from "../theme";
import { SETTINGS_TAB_IDS, type SettingsTabId, type TabRequest } from "../uiOpen";
import { AboutTab } from "./settings/AboutTab";
import { AccessTab } from "./settings/AccessTab";
import { DisplayTab } from "./settings/DisplayTab";
import { PairingTab } from "./settings/PairingTab";
import { SystemTab } from "./settings/SystemTab";

export interface SettingsSheetProps {
  session: Session | null;
  client: Client | null;
  /** Desktop: open dedicated Pair phone sheet from Settings. */
  onOpenPair?: () => void;
  /** Phone/web: open Session sheet (never Pair phone). */
  onOpenSessions?: () => void;
  pref: ThemePref;
  actual: "light" | "dark";
  onPref: (p: ThemePref) => void;
  wrap: boolean;
  onWrap: (w: boolean) => void;
  termFont: {
    px: number;
    zoomIn: () => void;
    zoomOut: () => void;
    canZoomIn: boolean;
    canZoomOut: boolean;
  };
  /** Deep-link command from the tray menu; see TabRequest. */
  tabRequest?: TabRequest | null;
  onClose: () => void;
}

type TabId = SettingsTabId;

const TAB_KEY = "merino.settings.tab";

function readTab(): TabId {
  try {
    const raw = localStorage.getItem(TAB_KEY) as TabId | null;
    if (raw && SETTINGS_TAB_IDS.includes(raw)) return raw;
  } catch {
    /* private mode / storage disabled */
  }
  return "pairing";
}

/**
 * Settings as a router: pick an intent, get one screen.
 *
 * The shell owns nothing but the current tab. Visibility is decided from
 * CLIENT CAPABILITY alone — never from live host state — so the strip cannot
 * reshuffle under the user while a toggle resolves, and no tab's state has to
 * be hoisted here just so the shell can count blocks.
 */
export function SettingsSheet({
  session,
  client,
  onOpenPair,
  onOpenSessions,
  pref,
  actual,
  onPref,
  wrap,
  onWrap,
  termFont,
  tabRequest,
  onClose,
}: SettingsSheetProps) {
  const isDesktop = client?.kind === "desktop";

  const [tab, setTab] = useState<TabId>(() => tabRequest?.tab ?? readTab());
  const selectTab = useCallback((id: TabId) => {
    setTab(id);
    try {
      localStorage.setItem(TAB_KEY, id);
    } catch {
      /* private mode / storage disabled */
    }
  }, []);

  // The sheet stays mounted while open, so a second deep link (tray menu →
  // Check for Updates… while Settings is already up) has to move the tab too.
  useEffect(() => {
    if (tabRequest) selectTab(tabRequest.tab);
    // Keyed on the whole request: a repeat of the same tab is a new object
    // with a new seq, so the menu item works every time, not just the first.
  }, [tabRequest, selectTab]);

  const tabs: { id: TabId; label: string; visible: boolean; render: () => React.ReactNode }[] = [
    {
      id: "pairing",
      label: "Pairing",
      visible: Boolean(client?.mintPairing || client?.listDevices),
      render: () => <PairingTab client={client} onOpenPair={onOpenPair} />,
    },
    {
      id: "access",
      label: "Access",
      visible:
        Boolean(client?.setPasswordLoginEnabled || client?.passwordLoginEnabled) ||
        (isDesktop && Boolean(client?.setAllowWritesEnabled || client?.setSessionSwitchEnabled)),
      render: () => <AccessTab client={client} session={session} isDesktop={isDesktop} />,
    },
    {
      id: "display",
      label: "Display",
      visible: true,
      render: () => (
        <DisplayTab
          pref={pref}
          actual={actual}
          onPref={onPref}
          wrap={wrap}
          onWrap={onWrap}
          termFont={termFont}
        />
      ),
    },
    {
      id: "system",
      label: "System",
      visible:
        (isDesktop && Boolean(client?.setLaunchAtLogin || client?.checkUpdate)) ||
        Boolean(client?.pushSubscribe),
      render: () => <SystemTab client={client} isDesktop={isDesktop} />,
    },
    {
      id: "about",
      label: "About",
      visible: true,
      render: () => (
        <AboutTab
          client={client}
          session={session}
          isDesktop={isDesktop}
          onOpenSessions={onOpenSessions}
        />
      ),
    },
  ];

  const available = tabs.filter((t) => t.visible);
  const active = available.find((t) => t.id === tab) ?? available[0];


  return (
    <Sheet
      title="Settings"
      subtitle={
        isDesktop
          ? `Desktop · ${session?.user ?? "local"}`
          : `Browser · ${session?.user ?? "—"}`
      }
      panelClass="sheet--settings"
      onClose={onClose}
      toolbar={
        <div
          className="settings-tabs"
          role="tablist"
          aria-label="Settings sections"
          onKeyDown={(e) => {
            const step =
              e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
            if (!step && e.key !== "Home" && e.key !== "End") return;
            e.preventDefault();
            const i = available.findIndex((t) => t.id === active.id);
            const next =
              e.key === "Home"
                ? 0
                : e.key === "End"
                  ? available.length - 1
                  : (i + step + available.length) % available.length;
            selectTab(available[next].id);
            // Follow-focus: an arrow key moves the tab AND the focus ring, so
            // the next arrow keeps working without a Tab press.
            document.getElementById(`settings-tab-${available[next].id}`)?.focus();
          }}
        >
          {available.map((t) => (
            <button
              key={t.id}
              id={`settings-tab-${t.id}`}
              type="button"
              role="tab"
              aria-selected={t.id === active.id}
              aria-controls={`settings-pane-${t.id}`}
              tabIndex={t.id === active.id ? 0 : -1}
              className={`settings-tabs__tab${t.id === active.id ? " is-on" : ""}`}
              onClick={() => selectTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
      }
    >
      <div
        key={active.id}
        id={`settings-pane-${active.id}`}
        role="tabpanel"
        aria-labelledby={`settings-tab-${active.id}`}
        className="settings-pane"
      >
        {active.render()}
      </div>
    </Sheet>
  );
}

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import type { Agent } from "../bindings/github.com/LoneExile/merino/internal/app";
import type { Client, HerdrSession, AccessOrigin, PairedDevice, PairingTicket, RenameKind, Session, SessionList, UpdateInfo } from "./client";
import { Sheet } from "./Sheet";
import type { ThemePref } from "./theme";
import { SETTINGS_TAB_IDS, type SettingsTabId, type TabRequest } from "./uiOpen";

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

const THEMES: { id: ThemePref; label: string }[] = [
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
  { id: "system", label: "System" },
];

const WRAP_OPTS: { value: boolean; label: string }[] = [
  { value: false, label: "Off" },
  { value: true, label: "On" },
];

type PushStatus = "checking" | "unsupported" | "denied" | "off" | "subscribed";

type TabId = SettingsTabId;

interface SettingsTab {
  id: TabId;
  label: string;
  /** Blocks this tab would actually render. Zero means the tab is hidden. */
  blocks: number;
  body: ReactNode;
}

const count = (...flags: boolean[]) => flags.filter(Boolean).length;

const TAB_KEY = "merino.settings.tab";

const TAB_IDS = SETTINGS_TAB_IDS;

function readTab(): TabId {
  try {
    const raw = localStorage.getItem(TAB_KEY) as TabId | null;
    if (raw && TAB_IDS.includes(raw)) return raw;
  } catch {
    /* private mode / storage disabled */
  }
  return "pairing";
}

// Feature-detects rather than trusting client.pushSubscribe alone: a server
// with push enabled says nothing about whether THIS browser can act on it.
async function checkPushStatus(client: Client | null): Promise<PushStatus> {
  if (!client?.pushSubscribe || !("serviceWorker" in navigator) || !("PushManager" in window)) {
    return "unsupported";
  }
  if (Notification.permission === "denied") return "denied";
  try {
    const registration = await navigator.serviceWorker.ready;
    const subscription = await registration.pushManager.getSubscription();
    return subscription ? "subscribed" : "off";
  } catch {
    return "unsupported";
  }
}

// PushManager.subscribe wants the VAPID public key as a raw byte array, not
// the base64url string the server hands back — the standard conversion
// every Web Push integration needs.
function urlBase64ToUint8Array(base64Url: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64Url.length % 4)) % 4);
  const base64 = (base64Url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

/** Short label for Settings — never dump a raw User-Agent. */
function displaySessionName(session: Session | null | undefined): string {
  if (!session) return "—";
  const u = session.user?.trim() || "";
  if (session.provider === "pairing" || /mozilla|applewebkit|android|iphone/i.test(u)) {
    return friendlyUA(u) || "Phone";
  }
  return u || "—";
}

function friendlyUA(ua: string): string {
  const l = ua.toLowerCase();
  if (l.includes("iphone") || l.includes("crios") || l.includes("fxios")) return "iPhone";
  if (l.includes("ipad")) return "iPad";
  if (l.includes("android")) return l.includes("tablet") ? "Android tablet" : "Android phone";
  if (l.includes("mac")) return "Mac browser";
  if (l.includes("windows")) return "Windows browser";
  if (!ua || /mozilla|webkit/i.test(ua)) return "Phone";
  return ua.length > 28 ? `${ua.slice(0, 28)}…` : ua;
}

function displayDeviceName(name: string): string {
  if (/mozilla|applewebkit|android|iphone/i.test(name)) return friendlyUA(name);
  return name || "Phone";
}

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
  const [pushStatus, setPushStatus] = useState<PushStatus>("checking");
  const [pushErr, setPushErr] = useState<string | null>(null);
  const [pushBusy, setPushBusy] = useState(false);

  const isDesktop = client?.kind === "desktop";

  const [loginLaunch, setLoginLaunch] = useState<boolean | null>(null);
  const [loginLaunchErr, setLoginLaunchErr] = useState<string | null>(null);
  const [loginLaunchBusy, setLoginLaunchBusy] = useState(false);

  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  const [updateErr, setUpdateErr] = useState<string | null>(null);
  const [updateBusy, setUpdateBusy] = useState(false);

  const [devices, setDevices] = useState<PairedDevice[]>([]);
  const [devBusy, setDevBusy] = useState(false);
  const [devErr, setDevErr] = useState<string | null>(null);
  const [phoneUser, setPhoneUser] = useState("phone");
  const [phonePass, setPhonePass] = useState("");
  const [passMsg, setPassMsg] = useState<string | null>(null);
  const [passwordLoginOn, setPasswordLoginOn] = useState(true);
  const [sessionSwitchOn, setSessionSwitchOn] = useState(false);
  const [sessionSwitchBusy, setSessionSwitchBusy] = useState(false);
  const [allowWritesOn, setAllowWritesOn] = useState(false);
  const [allowWritesBusy, setAllowWritesBusy] = useState(false);
  const [writesErr, setWritesErr] = useState<string | null>(null);
  const [pwLoginBusy, setPwLoginBusy] = useState(false);
  const [panicArmed, setPanicArmed] = useState(false);
  const [sessionErr, setSessionErr] = useState<string | null>(null);

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

  const refreshDevices = useCallback(() => {
    if (!client?.listDevices) return;
    void client
      .listDevices()
      .then((r) => {
        const list = (r.devices || []).map((d) => {
          const rev = d.revokedAt as unknown;
          // Wails may send null, "", or Go zero time.
          const revoked =
            rev &&
            rev !== "0001-01-01T00:00:00Z" &&
            rev !== "0001-01-01T00:00:00.000Z" &&
            String(rev) !== "null";
          return { ...d, revokedAt: revoked ? String(rev) : null };
        });
        setDevices(list);
      })
      .catch((e) => setDevErr(e instanceof Error ? e.message : String(e)));
  }, [client]);

  useEffect(() => {
    refreshDevices();
  }, [refreshDevices]);



  useEffect(() => {
    if (!client?.launchAtLogin) return;
    let alive = true;
    void client.launchAtLogin().then(
      (on) => {
        if (alive) setLoginLaunch(on);
      },
      (e: unknown) => {
        if (alive) setLoginLaunchErr(e instanceof Error ? e.message : String(e));
      },
    );
    return () => {
      alive = false;
    };
  }, [client]);

  // Load Mac Settings toggles from the live host (disk + gate).
  useEffect(() => {
    if (!client) return;
    let alive = true;
    if (client.sessionSwitchEnabled) {
      void client.sessionSwitchEnabled().then(
        (on) => {
          if (alive) setSessionSwitchOn(on);
        },
        () => undefined,
      );
    }
    if (client.allowWritesEnabled) {
      void client.allowWritesEnabled().then(
        (on) => {
          if (alive) setAllowWritesOn(on);
        },
        () => undefined,
      );
    }
    if (client.passwordLoginEnabled) {
      void client.passwordLoginEnabled().then(
        (on) => {
          if (alive) setPasswordLoginOn(on);
        },
        () => undefined,
      );
    }
    return () => {
      alive = false;
    };
  }, [client]);

  const enableNotifications = async () => {
    const pushKey = client?.pushKey;
    const pushSubscribe = client?.pushSubscribe;
    if (!pushKey || !pushSubscribe || pushBusy) return;
    setPushBusy(true);
    setPushErr(null);
    try {
      // MUST run inside this click handler: iOS silently refuses
      // Notification.requestPermission() called any other way (e.g. from an
      // effect on mount), leaving permission stuck at "default" forever.
      const permission = await Notification.requestPermission();
      if (permission !== "granted") {
        setPushStatus(await checkPushStatus(client));
        return;
      }
      const registration = await navigator.serviceWorker.ready;
      const key = await pushKey();
      const subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(key),
      });
      await pushSubscribe(subscription.toJSON());
      setPushStatus("subscribed");
    } catch (e) {
      setPushErr(e instanceof Error ? e.message : String(e));
      setPushStatus(await checkPushStatus(client));
    } finally {
      setPushBusy(false);
    }
  };

  const disableNotifications = async () => {
    const pushUnsubscribe = client?.pushUnsubscribe;
    if (!pushUnsubscribe || pushBusy) return;
    setPushBusy(true);
    setPushErr(null);
    try {
      const registration = await navigator.serviceWorker.ready;
      const subscription = await registration.pushManager.getSubscription();
      if (subscription) {
        await pushUnsubscribe(subscription.endpoint);
        await subscription.unsubscribe();
      }
      setPushStatus("off");
    } catch (e) {
      setPushErr(e instanceof Error ? e.message : String(e));
    } finally {
      setPushBusy(false);
    }
  };



  // Which blocks exist at all on this client. Computed once so the tab strip
  // can hide a section that would open empty — a phone has no Mac settings,
  // and a tab that leads nowhere is worse than no tab.
  const showPair = isDesktop && Boolean(client?.mintPairing || onOpenPair);
  const showDevices = Boolean(client?.listDevices);
  const showWrites = isDesktop && Boolean(client?.setAllowWritesEnabled);
  const showSwitch = isDesktop && Boolean(client?.setSessionSwitchEnabled);
  const showPwLogin = Boolean(client?.setPasswordLoginEnabled || client?.passwordLoginEnabled);
  const showPhonePass = Boolean(client?.setOptionalPassword) && passwordLoginOn;
  const showMac = isDesktop && Boolean(client?.setLaunchAtLogin || client?.checkUpdate);
  const showPush = Boolean(client?.pushSubscribe);

  const tabs: SettingsTab[] = [
    {
      id: "pairing",
      label: "Pairing",
      blocks: count(showPair, showDevices),
      body: (
        <>
          {showPair && (
            <section className="settings-block" aria-labelledby="set-pair">
              <header className="settings-block__head">
                <h3 id="set-pair">Pair phone</h3>
              </header>
              <p className="settings-copy settings-copy--quiet">
                Mint a one-shot QR for a phone on this network.
              </p>
              <button type="button" className="btn btn--solid" onClick={() => onOpenPair?.()}>
                Show pair QR
              </button>
            </section>
          )}

          {showDevices && (
            <section className="settings-block" aria-labelledby="set-devices">
              <header className="settings-block__head">
                <h3 id="set-devices">Paired devices</h3>
              </header>
              <p className="settings-copy">
                Each phone that scans a QR gets its own grant. Revoke a lost phone without rotating the Mac password.
              </p>
              {devices.length === 0 && (
                <p className="settings-copy settings-copy--quiet">No phones paired yet.</p>
              )}
              <ul className="list list--plain">
                {devices.map((d) => (
                  <li key={d.id}>
                    <div className={`row row--session${d.revokedAt ? "" : " is-on"}`}>
                      <span className="row__main">
                        <span className="row__title">{displayDeviceName(d.name || "")}</span>
                        <span className="row__sub mono">
                          {d.provider}
                          {d.revokedAt ? " · revoked" : " · active"}
                          {" · "}
                          {d.id.slice(0, 8)}
                        </span>
                      </span>
                      {!d.revokedAt && client?.revokeDevice && (
                        <button
                          type="button"
                          className="btn btn--icon"
                          disabled={devBusy}
                          aria-label={`Revoke ${d.name || d.id}`}
                          title="Revoke"
                          onClick={() => {
                            setDevBusy(true);
                            setDevErr(null);
                            void client.revokeDevice?.(d.id)
                              .then(() => refreshDevices())
                              .catch((e) => setDevErr(e instanceof Error ? e.message : String(e)))
                              .finally(() => setDevBusy(false));
                          }}
                        >
                          ×
                        </button>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
              {client?.revokeAllDevices && devices.some((d) => !d.revokedAt) && (
                <div className="panic-row">
                  {!panicArmed ? (
                    <button
                      type="button"
                      className="btn"
                      disabled={devBusy}
                      onClick={() => setPanicArmed(true)}
                    >
                      Panic revoke all phones
                    </button>
                  ) : (
                    <>
                      <p className="settings-copy settings-copy--warn">
                        Revoke every active phone grant? They must scan a new QR.
                      </p>
                      <div className="panic-row__actions">
                        <button
                          type="button"
                          className="btn"
                          disabled={devBusy}
                          onClick={() => setPanicArmed(false)}
                        >
                          Cancel
                        </button>
                        <button
                          type="button"
                          className="btn btn--signout"
                          disabled={devBusy}
                          onClick={() => {
                            setDevBusy(true);
                            setDevErr(null);
                            void client
                              .revokeAllDevices?.()
                              .then((n) => {
                                setPanicArmed(false);
                                refreshDevices();
                                void client.markFirstRunDone?.();
                                if (typeof n === "number" && n === 0) {
                                  setDevErr("No devices were revoked (store may be out of sync).");
                                }
                              })
                              .catch((e) => setDevErr(e instanceof Error ? e.message : String(e)))
                              .finally(() => setDevBusy(false));
                          }}
                        >
                          {devBusy ? "Revoking…" : "Confirm revoke all"}
                        </button>
                      </div>
                    </>
                  )}
                </div>
              )}
              {devErr && (
                <p className="composer__err" role="alert">
                  {devErr}
                </p>
              )}
            </section>
          )}
        </>
      ),
    },
    {
      id: "access",
      label: "Access",
      blocks: count(showWrites, showSwitch, showPwLogin, showPhonePass),
      body: (
        <>
          {showWrites && (
            <section className="settings-block" aria-labelledby="set-writes">
              <header className="settings-block__head">
                <h3 id="set-writes">Phone writes</h3>
              </header>
              <div className="settings-row settings-row--toggle">
                <div className="settings-row__meta">
                  <span className="settings-row__label">Allow phone writes</span>
                  <span className="settings-row__hint">
                    Phones can answer asks, type, and interrupt agents (audit-logged)
                  </span>
                </div>
                <label className="switch">
                  <input
                    type="checkbox"
                    checked={allowWritesOn}
                    disabled={allowWritesBusy}
                    onChange={(e) => {
                      const on = e.target.checked;
                      setAllowWritesBusy(true);
                      setWritesErr(null);
                      void client
                        ?.setAllowWritesEnabled?.(on)
                        .then(() => setAllowWritesOn(on))
                        .catch((err) =>
                          setWritesErr(err instanceof Error ? err.message : String(err)),
                        )
                        .finally(() => setAllowWritesBusy(false));
                    }}
                  />
                  <span className="switch__ui" />
                </label>
              </div>
              {writesErr && (
                <p className="composer__err" role="alert">
                  {writesErr}
                </p>
              )}
              {allowWritesOn && (
                <p className="settings-copy settings-copy--warn">
                  Paired phones can type into live terminals. Every write is recorded in the host audit log.
                </p>
              )}
            </section>
          )}

          {showSwitch && (
            <section className="settings-block" aria-labelledby="set-session-switch">
              <header className="settings-block__head">
                <h3 id="set-session-switch">Session switch</h3>
              </header>
              <div className="settings-row settings-row--toggle">
                <div className="settings-row__meta">
                  <span className="settings-row__label">Allow session switch</span>
                  <span className="settings-row__hint">
                    Phones can change which herdr session this drives
                  </span>
                </div>
                <label className="switch">
                  <input
                    type="checkbox"
                    checked={sessionSwitchOn}
                    disabled={sessionSwitchBusy}
                    onChange={(e) => {
                      const on = e.target.checked;
                      setSessionSwitchBusy(true);
                      setSessionErr(null);
                      void client
                        ?.setSessionSwitchEnabled?.(on)
                        .then(() => setSessionSwitchOn(on))
                        .catch((err) =>
                          setSessionErr(err instanceof Error ? err.message : String(err)),
                        )
                        .finally(() => setSessionSwitchBusy(false));
                    }}
                  />
                  <span className="switch__ui" />
                </label>
              </div>
              {sessionErr && (
                <p className="composer__err" role="alert">
                  {sessionErr}
                </p>
              )}
            </section>
          )}

          {showPwLogin && (
            <section className="settings-block" aria-labelledby="set-pw-login">
              <header className="settings-block__head">
                <h3 id="set-pw-login">Password sign-in</h3>
              </header>
              <div className="settings-row settings-row--toggle">
                <div className="settings-row__meta">
                  <span className="settings-row__label">Allow username / password</span>
                  <span className="settings-row__hint">Off by default: phones pair with QR only</span>
                </div>
                <label className="switch">
                  <input
                    type="checkbox"
                    checked={passwordLoginOn}
                    disabled={pwLoginBusy || !client?.setPasswordLoginEnabled}
                    onChange={(e) => {
                      const on = e.target.checked;
                      setPwLoginBusy(true);
                      void client
                        ?.setPasswordLoginEnabled?.(on)
                        .then(() => setPasswordLoginOn(on))
                        .catch(() => {})
                        .finally(() => setPwLoginBusy(false));
                    }}
                  />
                  <span className="switch__ui" />
                </label>
              </div>
            </section>
          )}

          {showPhonePass && (
            <section className="settings-block" aria-labelledby="set-phone-pass">
              <header className="settings-block__head">
                <h3 id="set-phone-pass">Phone password</h3>
              </header>
              <p className="settings-copy">
                Lets a browser sign in with user/pass when you are off the home Wi‑Fi (with a public URL). Leave blank and save to clear.
              </p>
              <label className="field__label" htmlFor="phone-user">
                Username
              </label>
              <input
                id="phone-user"
                className="field__input"
                value={phoneUser}
                onChange={(e) => setPhoneUser(e.target.value)}
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
              />
              <label className="field__label" htmlFor="phone-pass">
                Password
              </label>
              <input
                id="phone-pass"
                className="field__input"
                type="password"
                value={phonePass}
                onChange={(e) => setPhonePass(e.target.value)}
                autoComplete="new-password"
              />
              <button
                type="button"
                className="btn btn--solid"
                onClick={() => {
                  setPassMsg(null);
                  void client?.setOptionalPassword?.(phoneUser.trim() || "phone", phonePass)
                    .then(() => setPassMsg(phonePass ? "Phone password saved." : "Phone password cleared."))
                    .catch((e) => setPassMsg(e instanceof Error ? e.message : String(e)));
                }}
              >
                Save phone password
              </button>
              {session?.oidcEnabled && (
                <p className="settings-copy settings-copy--quiet">
                  OAuth is configured on the server (scaffold). Full provider login lands in a follow-up.
                </p>
              )}
              {passMsg && <p className="settings-copy">{passMsg}</p>}
            </section>
          )}
        </>
      ),
    },
    {
      id: "display",
      label: "Display",
      blocks: 1,
      body: (
        <section className="settings-block" aria-labelledby="set-appear">
          <header className="settings-block__head">
            <h3 id="set-appear">Appearance</h3>
          </header>
          <div className="settings-row">
            <div className="settings-row__meta">
              <span className="settings-row__label">Theme</span>
              <span className="settings-row__hint">
                {pref === "system" ? `Follows device · ${actual}` : `Locked · ${pref}`}
              </span>
            </div>
            <div className="seg seg--compact" role="radiogroup" aria-label="Theme">
              {THEMES.map((t) => (
                <button
                  key={t.id}
                  type="button"
                  role="radio"
                  aria-checked={pref === t.id}
                  className={`seg__opt${pref === t.id ? " is-on" : ""}`}
                  onClick={() => onPref(t.id)}
                >
                  {t.label}
                </button>
              ))}
            </div>
          </div>
          <div className="settings-row">
            <div className="settings-row__meta">
              <span className="settings-row__label">Line wrap</span>
              <span className="settings-row__hint">
                {wrap ? "Long lines fold to the pane width" : "Scroll sideways for long lines"}
              </span>
            </div>
            <div className="seg seg--compact" role="radiogroup" aria-label="Wrap long lines">
              {WRAP_OPTS.map((o) => (
                <button
                  key={String(o.value)}
                  type="button"
                  role="radio"
                  aria-checked={wrap === o.value}
                  className={`seg__opt${wrap === o.value ? " is-on" : ""}`}
                  onClick={() => onWrap(o.value)}
                >
                  {o.label}
                </button>
              ))}
            </div>
          </div>
          <div className="settings-row">
            <div className="settings-row__meta">
              <span className="settings-row__label">Terminal size</span>
              <span className="settings-row__hint">{termFont.px}px monospaced</span>
            </div>
            <div className="seg seg--compact" role="group" aria-label="Terminal font size">
              <button
                type="button"
                className="seg__opt"
                disabled={!termFont.canZoomOut}
                onClick={() => termFont.zoomOut()}
                aria-label="Decrease font size"
              >
                A−
              </button>
              <button
                type="button"
                className="seg__opt"
                disabled={!termFont.canZoomIn}
                onClick={() => termFont.zoomIn()}
                aria-label="Increase font size"
              >
                A+
              </button>
            </div>
          </div>
        </section>
      ),
    },
    {
      id: "system",
      label: "System",
      blocks: count(showMac, showPush),
      body: (
        <>
          {showMac && (
            <section className="settings-block" aria-labelledby="set-machine">
              <header className="settings-block__head">
                <h3 id="set-machine">This Mac</h3>
              </header>
              {client?.setLaunchAtLogin && (
                <div className="settings-row settings-row--toggle">
                  <div className="settings-row__meta">
                    <span className="settings-row__label">Launch at login</span>
                    <span className="settings-row__hint">Open with your user session</span>
                  </div>
                  <label className="switch">
                    <input
                      type="checkbox"
                      checked={loginLaunch === true}
                      disabled={loginLaunchBusy || loginLaunch === null}
                      onChange={(e) => {
                        const on = e.target.checked;
                        setLoginLaunchBusy(true);
                        setLoginLaunchErr(null);
                        void client
                          .setLaunchAtLogin?.(on)
                          .then(
                            () => setLoginLaunch(on),
                            (err: unknown) =>
                              setLoginLaunchErr(err instanceof Error ? err.message : String(err)),
                          )
                          .finally(() => setLoginLaunchBusy(false));
                      }}
                    />
                    <span className="switch__ui" aria-hidden="true" />
                    <span className="sr-only">Launch at login</span>
                  </label>
                </div>
              )}
              {loginLaunchErr && (
                <p className="composer__err" role="alert">
                  {loginLaunchErr}
                </p>
              )}
              {client?.checkUpdate && (
                <div className="settings-stack">
                  <button
                    type="button"
                    className="btn"
                    disabled={updateBusy}
                    onClick={() => {
                      setUpdateBusy(true);
                      setUpdateErr(null);
                      void client
                        .checkUpdate?.()
                        .then(
                          (info) => setUpdate(info),
                          (err: unknown) =>
                            setUpdateErr(err instanceof Error ? err.message : String(err)),
                        )
                        .finally(() => setUpdateBusy(false));
                    }}
                  >
                    {updateBusy ? "Checking…" : "Check for updates"}
                  </button>
                  {update && (
                    <dl className="facts facts--dense">
                      <div>
                        <dt>Installed</dt>
                        <dd className="mono">{update.current || "—"}</dd>
                      </div>
                      <div>
                        <dt>Latest</dt>
                        <dd className="mono">{update.latest || "—"}</dd>
                      </div>
                    </dl>
                  )}
                  {update?.newer && (
                    <div className="settings-stack">
                      {client.installUpdate && update.canInstall && (
                        <button
                          type="button"
                          className="btn btn--primary"
                          disabled={updateBusy}
                          onClick={() => {
                            setUpdateBusy(true);
                            setUpdateErr(null);
                            void client
                              .installUpdate?.()
                              .then(
                                (r) => {
                                  // Process should quit + relaunch; surface message if not.
                                  setUpdateErr(null);
                                  setUpdate((u) =>
                                    u
                                      ? {
                                          ...u,
                                          body: r.message || `Installing ${r.version}…`,
                                        }
                                      : u,
                                  );
                                },
                                (err: unknown) =>
                                  setUpdateErr(err instanceof Error ? err.message : String(err)),
                              )
                              .finally(() => setUpdateBusy(false));
                          }}
                        >
                          {updateBusy ? "Installing…" : `Install ${update.latest}`}
                        </button>
                      )}
                      <p className="settings-copy">
                        <a href={update.releaseUrl} target="_blank" rel="noreferrer">
                          Open release {update.latest} ↗
                        </a>
                      </p>
                    </div>
                  )}
                  {update && !update.newer && update.latest && (
                    <p className="settings-copy settings-copy--quiet">Up to date.</p>
                  )}
                  {updateErr && (
                    <p className="composer__err" role="alert">
                      {updateErr}
                    </p>
                  )}
                </div>
              )}
            </section>
          )}

          {showPush && (
            <section className="settings-block" aria-labelledby="set-push">
              <header className="settings-block__head">
                <h3 id="set-push">Alerts</h3>
                <span
                  className={`settings-pill${
                    pushStatus === "subscribed"
                      ? " settings-pill--ok"
                      : pushStatus === "denied"
                        ? " settings-pill--warn"
                        : ""
                  }`}
                >
                  {pushStatus === "checking"
                    ? "…"
                    : pushStatus === "subscribed"
                      ? "On"
                      : pushStatus === "denied"
                        ? "Blocked"
                        : pushStatus === "unsupported"
                          ? "N/A"
                          : "Off"}
                </span>
              </header>
              {pushStatus === "unsupported" && (
                <p className="settings-copy">Push is not available in this browser.</p>
              )}
              {pushStatus === "denied" && (
                <p className="settings-copy settings-copy--warn">
                  Notifications are blocked. Allow them for this site in system settings, then reopen.
                </p>
              )}
              {(pushStatus === "off" || pushStatus === "checking") && (
                <button
                  type="button"
                  className="btn btn--solid"
                  disabled={pushBusy || pushStatus === "checking"}
                  onClick={() => void enableNotifications()}
                >
                  {pushBusy ? "Enabling…" : "Enable notifications"}
                </button>
              )}
              {pushStatus === "subscribed" && (
                <>
                  <p className="settings-copy">
                    Notified the moment an agent needs you — even with the app closed.
                  </p>
                  <button
                    type="button"
                    className="btn"
                    disabled={pushBusy}
                    onClick={() => void disableNotifications()}
                  >
                    Turn off
                  </button>
                </>
              )}
              {pushErr && (
                <p className="composer__err" role="alert">
                  {pushErr}
                </p>
              )}
              <p className="settings-copy settings-copy--quiet">
                iPhone: Home Screen install required (Share → Add to Home Screen). Safari tabs do not
                receive push.
              </p>
            </section>
          )}
        </>
      ),
    },
    {
      id: "about",
      label: "About",
      blocks: 2,
      body: (
        <>
          <section className="settings-block" aria-labelledby="set-conn">
            <header className="settings-block__head">
              <h3 id="set-conn">Connection</h3>
              {!session?.readOnly && (
                <span className="settings-pill settings-pill--warn">Writes on</span>
              )}
              {session?.readOnly && <span className="settings-pill">Read-only</span>}
            </header>
            <dl className="facts facts--dense facts--connection">
              <div>
                <dt>Signed in as</dt>
                <dd title={session?.user}>{displaySessionName(session)}</dd>
              </div>
              <div>
                <dt>Auth</dt>
                <dd className="mono">{session?.provider ?? "—"}</dd>
              </div>
              <div>
                <dt>Transport</dt>
                <dd className="mono">{client?.kind ?? "—"}</dd>
              </div>
              <div>
                <dt>Live output</dt>
                <dd className="mono">{client?.streamPane ? "stream" : "poll"}</dd>
              </div>
            </dl>
            {!session?.readOnly && (
              <p className="settings-copy settings-copy--warn">
                This dashboard can type into terminals. Writes are recorded in the host audit log.
              </p>
            )}
          </section>

          <section className="settings-block" aria-labelledby="set-keys">
            <header className="settings-block__head">
              <h3 id="set-keys">Shortcuts</h3>
            </header>
            <ul className="settings-keys">
              <li>
                <span className="settings-keys__combo">
                  <kbd>⌘</kbd>
                  <kbd>K</kbd>
                </span>
                <span className="settings-keys__label">Jump to agent</span>
              </li>
              <li>
                <span className="settings-keys__combo">
                  <kbd>Enter</kbd>
                </span>
                <span className="settings-keys__label">Send reply</span>
              </li>
              <li>
                <span className="settings-keys__combo">
                  <kbd>⇧</kbd>
                  <kbd>Enter</kbd>
                </span>
                <span className="settings-keys__label">Newline</span>
              </li>
              <li>
                <span className="settings-keys__combo">
                  <kbd>Esc</kbd>
                </span>
                <span className="settings-keys__label">Close / back</span>
              </li>
              <li>
                <span className="settings-keys__combo">
                  <kbd>⌘</kbd>
                  <kbd>F</kbd>
                </span>
                <span className="settings-keys__label">Find in pane</span>
              </li>
              <li>
                <span className="settings-keys__combo">
                  <kbd>⌘</kbd>
                  <kbd>+</kbd>
                  <span className="settings-keys__sep">/</span>
                  <kbd>−</kbd>
                </span>
                <span className="settings-keys__label">Terminal font size</span>
              </li>
            </ul>
          </section>

          {!isDesktop && (
            <div className="sheet__foot">
              {client?.sessions && onOpenSessions && (
                <button type="button" className="btn btn--primary" onClick={onOpenSessions}>
                  Change session
                </button>
              )}
              <form method="post" action="/logout">
                {/* POST so a stray GET cannot CSRF-logout. */}
                <button type="submit" className="btn btn--signout">
                  Sign out
                </button>
              </form>
            </div>
          )}
        </>
      ),
    },
  ];

  const available = tabs.filter((t) => t.blocks > 0);
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
        className={`settings-pane${active.blocks === 1 ? " settings-pane--single" : ""}`}
      >
        {active.body}
      </div>
    </Sheet>
  );
}


export interface SessionSheetProps {
  client: Client;
  onClose: () => void;
}

/**
 * herdr session picker.
 *
 * Switching is a privileged act — it repoints the dashboard at a different set
 * of live terminals — so the control only exists when the operator started the
 * server with it enabled. When it is off this sheet still lists what is running,
 * because knowing which session you are driving matters even when you cannot
 * change it.
 */
export function SessionSheet({ client, onClose }: SessionSheetProps) {
  const [data, setData] = useState<SessionList | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    if (!client.sessions) {
      // Defensive: the palette no longer offers this without the capability,
      // but a sheet that waits on a method the transport does not implement
      // shows "Looking for sessions…" forever, which is what the desktop
      // panel did.
      setErr("This build cannot list herdr sessions.");
      return;
    }
    void (async () => {
      try {
        const d = await client.sessions?.();
        if (alive && d) setData(d);
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      alive = false;
    };
  }, [client]);

  const pick = async (s: HerdrSession) => {
    if (s.current || busy !== null) return;
    if (!client.switchSession) {
      setErr("This build cannot switch herdr sessions.");
      return;
    }
    setBusy(s.id);
    setErr(null);
    try {
      await client.switchSession(s.id);
      // Full reload: agent list + streams belong to the old session.
      // Keep path only — never re-apply ?pair=1 from a first-run cold start.
      const next = `${window.location.pathname}${window.location.hash}`;
      window.location.replace(next || "/");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(null);
    }
  };

  return (
    <Sheet title="Session" onClose={onClose}>
      {err && (
        <p className="composer__err" role="alert">
          {err}
        </p>
      )}
      {!data && !err && <p className="hint">Looking for sessions…</p>}

      <ul className="list list--plain">
        {data?.sessions.map((s) => (
          <li key={s.id}>
            <button
              type="button"
              className={`row row--session${s.current ? " is-on" : ""}`}
              disabled={s.current || !s.reachable || busy !== null}
              onClick={() => void pick(s)}
            >
              <span className="row__main">
                <span className="row__title">{s.name}</span>
                <span className="mono row__sub">
                  {s.reachable ? `${s.panes} panes · ${s.agents} agents` : "not responding"}
                </span>
              </span>
              {s.current && <span className="chip mono">current</span>}
              {busy === s.id && <span className="chip mono">switching…</span>}
            </button>
          </li>
        ))}
      </ul>

      {data && !data.canSwitch && (
        <p className="hint">
          Switching is off. Enable it under Settings on the Mac menu bar.
        </p>
      )}
    </Sheet>
  );
}

export interface RenameTarget {
  kind: RenameKind;
  id: string;
  current: string;
}

export function renameTargets(agent: Agent): RenameTarget[] {
  return [
    { kind: "pane", id: agent.paneId, current: agent.agent || agent.paneId },
    { kind: "tab", id: agent.tabId, current: agent.tabId },
    { kind: "workspace", id: agent.workspaceId, current: agent.workspaceId },
  ];
}

export interface RenameSheetProps {
  client: Client;
  agent: Agent;
  onClose: () => void;
}

export function RenameSheet({ client, agent, onClose }: RenameSheetProps) {
  const targets = renameTargets(agent);
  const [kind, setKind] = useState<RenameKind>("pane");
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const target = targets.find((t) => t.kind === kind)!;

  const submit = async () => {
    const next = name.trim();
    if (!next || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await client.rename?.(kind, target.id, next);
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <Sheet title="Rename" onClose={onClose}>
      <div className="seg" role="radiogroup" aria-label="What to rename">
        {targets.map((t) => (
          <button
            key={t.kind}
            role="radio"
            aria-checked={kind === t.kind}
            className={`seg__opt${kind === t.kind ? " is-on" : ""}`}
            onClick={() => setKind(t.kind)}
          >
            {t.kind}
          </button>
        ))}
      </div>

      <p className="hint mono">{target.id}</p>

      <label className="field">
        <span className="field__label">New name</span>
        <input
          className="input"
          value={name}
          maxLength={120}
          autoFocus
          placeholder={target.current}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void submit();
            }
          }}
        />
      </label>

      {err && (
        <p className="composer__err" role="alert">
          {err}
        </p>
      )}

      <div className="sheet__actions">
        <button className="btn" onClick={onClose}>
          Cancel
        </button>
        <button className="btn btn--primary" disabled={busy || !name.trim()} onClick={() => void submit()}>
          Rename {kind}
        </button>
      </div>
    </Sheet>
  );
}

export interface PairPhoneSheetProps {
  client: Client;
  onClose: () => void;
  /** Open full Settings (advanced). */
  onOpenSettings?: () => void;
}

/**
 * Dedicated tray "Pair phone…" surface: mint a one-shot QR without burying it
 * inside the full Settings sheet.
 */
export function PairPhoneSheet({ client, onClose, onOpenSettings }: PairPhoneSheetProps) {
  const [pair, setPair] = useState<PairingTicket | null>(null);
  const [pairErr, setPairErr] = useState<string | null>(null);
  const [pairBusy, setPairBusy] = useState(false);
  const [ready, setReady] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [pairBase, setPairBase] = useState(
    () => localStorage.getItem("herdr.pairBase") ?? "",
  );
  const [accessOrigins, setAccessOrigins] = useState<AccessOrigin[]>([]);
  const pairBaseRef = useRef(pairBase);
  pairBaseRef.current = pairBase;

  const mint = useCallback(
    (baseOverride?: string) => {
      if (!client.mintPairing) return;
      const base = (baseOverride ?? pairBaseRef.current).trim();
      setPairBusy(true);
      setPairErr(null);
      if (base) localStorage.setItem("herdr.pairBase", base);
      void (async () => {
        try {
          if (base && client.setPairingBaseURL) await client.setPairingBaseURL(base);
          const ticket = await client.mintPairing?.();
          if (ticket) {
            setPair(ticket);
            void client.markFirstRunDone?.();
          }
        } catch (err) {
          setPairErr(err instanceof Error ? err.message : String(err));
        } finally {
          setPairBusy(false);
        }
      })();
    },
    [client],
  );

  // Resolve origins, pick a base, then mint once so the QR is the first paint.
  useEffect(() => {
    let alive = true;
    void (async () => {
      let base = pairBaseRef.current.trim();
      try {
        const origins = client.accessOrigins ? await client.accessOrigins() : [];
        if (!alive) return;
        setAccessOrigins(origins ?? []);
        if (!base) {
          base =
            (client.defaultPairBase ? await client.defaultPairBase() : "") ||
            origins?.find((o) => o.kind === "lan")?.url ||
            origins?.find((o) => o.kind === "tailscale")?.url ||
            origins?.[0]?.url ||
            "";
          if (base) setPairBase(base);
        }
      } catch {
        /* origins optional */
      }
      if (!alive) return;
      setReady(true);
      mint(base || undefined);
    })();
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- open once
  }, [client]);

  const pickOrigin = (url: string) => {
    setPairBase(url);
    localStorage.setItem("herdr.pairBase", url);
    mint(url);
  };

  const hostLabel = pairBase.replace(/^https?:\/\//, "").replace(/\/$/, "") || "—";

  const dismiss = () => {
    // Closing without scanning still counts as "saw first-run" so the next
    // panel open is not forced back to /?pair=1.
    void client.markFirstRunDone?.();
    onClose();
  };

  return (
    <Sheet title="Pair phone" subtitle="Scan to sign in · 2 min" panelClass="sheet--pair" onClose={dismiss}>
      <div className="pair-hero" aria-live="polite">
        {pair ? (
          <img className="pair-hero__qr" src={pair.qrPng} alt="Sign-in QR code" width={220} height={220} />
        ) : (
          <div className="pair-hero__skeleton" aria-hidden="true">
            {pairBusy || !ready ? "Minting…" : "—"}
          </div>
        )}
        {pair && (
          <>
            <p className="pair-hero__token mono" title="Paste on the phone login page">
              {pair.token}
            </p>
            <p className="pair-hero__meta">
              Expires {new Date(pair.expiresAt * 1000).toLocaleTimeString()}
              <span className="pair-hero__dot" aria-hidden="true">
                ·
              </span>
              <span className="mono pair-hero__host">{hostLabel}</span>
            </p>
          </>
        )}
        {pairErr && (
          <p className="composer__err" role="alert">
            {pairErr}
          </p>
        )}
      </div>

      {accessOrigins.length > 0 && (
        <div className="pair-origins pair-origins--compact" role="group" aria-label="Network">
          {accessOrigins.map((o) => (
            <button
              key={o.kind + o.url}
              type="button"
              className={`btn pair-origins__chip${pairBase === o.url ? " is-on" : ""}`}
              title={o.hint || o.url}
              disabled={pairBusy}
              onClick={() => pickOrigin(o.url)}
            >
              <span className="pair-origins__label">{o.label}</span>
            </button>
          ))}
        </div>
      )}

      <div className="pair-toolbar">
        <button type="button" className="btn btn--solid" disabled={pairBusy} onClick={() => mint()}>
          {pairBusy ? "Minting…" : "Refresh QR"}
        </button>
        {onOpenSettings && (
          <button type="button" className="btn" onClick={onOpenSettings}>
            Settings
          </button>
        )}
        <button
          type="button"
          className="btn btn--ghost"
          aria-expanded={showAdvanced}
          onClick={() => setShowAdvanced((v) => !v)}
        >
          {showAdvanced ? "Hide URL" : "Edit URL"}
        </button>
      </div>

      {showAdvanced && (
        <div className="pair-advanced">
          <label className="field__label" htmlFor="pair-sheet-base">
            QR base URL
          </label>
          <input
            id="pair-sheet-base"
            className="field__input"
            value={pairBase}
            onChange={(e) => setPairBase(e.target.value)}
            onBlur={() => {
              const b = pairBase.trim();
              if (b) mint(b);
            }}
            placeholder="https://… or http://192.168.x.x:8730"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
          />
          <p className="settings-copy settings-copy--quiet">
            HTTPS unlocks in-page camera scan. LAN / Tailscale are paste-code only.
          </p>
        </div>
      )}
    </Sheet>
  );
}

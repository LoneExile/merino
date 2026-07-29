// Extracted from the 1052-line SettingsSheet, where every tab's state lived in
// one scope and all five JSX trees were built on every render. Each tab now
// owns exactly the state it uses, so a reader can follow one without holding
// the other four.

import { useEffect, useState } from "react";
import type { Client, UpdateInfo } from "../../client";
import { checkPushStatus, urlBase64ToUint8Array, type PushStatus } from "../push";

export interface SystemTabProps {
  client: Client | null;
  isDesktop: boolean;
}

export function SystemTab({ client, isDesktop }: SystemTabProps) {
  const [pushStatus, setPushStatus] = useState<PushStatus>("checking");
  const [pushErr, setPushErr] = useState<string | null>(null);
  const [pushBusy, setPushBusy] = useState(false);
  const [loginLaunch, setLoginLaunch] = useState<boolean | null>(null);
  const [loginLaunchErr, setLoginLaunchErr] = useState<string | null>(null);
  const [loginLaunchBusy, setLoginLaunchBusy] = useState(false);
  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  const [updateErr, setUpdateErr] = useState<string | null>(null);
  const [updateBusy, setUpdateBusy] = useState(false);

  const showMac = isDesktop && Boolean(client?.setLaunchAtLogin || client?.checkUpdate);
  const showPush = Boolean(client?.pushSubscribe);

  useEffect(() => {
    let alive = true;
    void checkPushStatus(client).then((s) => {
      if (alive) setPushStatus(s);
    });
    return () => {
      alive = false;
    };
  }, [client]);

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

  return (
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
  );
}

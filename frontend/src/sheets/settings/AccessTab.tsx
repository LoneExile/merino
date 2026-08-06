// Extracted from the 1052-line SettingsSheet, where every tab's state lived in
// one scope and all five JSX trees were built on every render. Each tab now
// owns exactly the state it uses, so a reader can follow one without holding
// the other four.

import { useEffect, useState } from "react";
import type { Client, Session } from "../../client";

export interface AccessTabProps {
  client: Client | null;
  session: Session | null;
  isDesktop: boolean;
}

export function AccessTab({ client, session, isDesktop }: AccessTabProps) {
  const [phoneUser, setPhoneUser] = useState("phone");
  const [phonePass, setPhonePass] = useState("");
  const [passMsg, setPassMsg] = useState<string | null>(null);
  // Seeded OFF to match the host default (PasswordLoginEnabled returns false
  // for a missing file). Seeding true painted this switch ON — above a hint
  // reading "Off by default" — for the frame before the host answered, which
  // misreports the state of the weakest door in the app.
  const [passwordLoginOn, setPasswordLoginOn] = useState(false);
  const [pwLoginBusy, setPwLoginBusy] = useState(false);
  const [sessionSwitchOn, setSessionSwitchOn] = useState(false);
  const [sessionSwitchBusy, setSessionSwitchBusy] = useState(false);
  const [sessionErr, setSessionErr] = useState<string | null>(null);
  const [allowWritesOn, setAllowWritesOn] = useState(false);
  const [allowWritesBusy, setAllowWritesBusy] = useState(false);
  const [writesErr, setWritesErr] = useState<string | null>(null);

  const showWrites = isDesktop && Boolean(client?.setAllowWritesEnabled);
  const showSwitch = isDesktop && Boolean(client?.setSessionSwitchEnabled);
  const showPwLogin = Boolean(client?.setPasswordLoginEnabled || client?.passwordLoginEnabled);
  const showPhonePass = Boolean(client?.setOptionalPassword) && passwordLoginOn;

  // Load the host's live gate values. Each is independent: a client that
  // cannot answer one still answers the others.
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

  return (
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
          {session?.oauthEnabled && (
            <p className="settings-copy settings-copy--quiet">
              Single sign-on is configured — sign in with GitHub or Keycloak from the login page.
            </p>
          )}
          {passMsg && <p className="settings-copy">{passMsg}</p>}
        </section>
      )}
    </>
  );
}

// Extracted from the 1052-line SettingsSheet, where every tab's state lived in
// one scope and all five JSX trees were built on every render. Each tab now
// owns exactly the state it uses, so a reader can follow one without holding
// the other four.

import { useCallback, useEffect, useState } from "react";
import type { Client, PairedDevice } from "../../client";
import { displayDeviceName } from "../names";

export interface PairingTabProps {
  client: Client | null;
  onOpenPair?: () => void;
}

export function PairingTab({ client, onOpenPair }: PairingTabProps) {
  const [devices, setDevices] = useState<PairedDevice[]>([]);
  const [devBusy, setDevBusy] = useState(false);
  const [devErr, setDevErr] = useState<string | null>(null);
  const [panicArmed, setPanicArmed] = useState(false);

  // Was gated on isDesktop, which hid minting from the headless dashboard
  // even though merinod serves /api/pairing/mint and returns a rendered QR.
  // Gate on the capability instead: a paired phone has no mintPairing (the
  // server withholds canManageDevices from a device subject), so it still
  // cannot mint another phone.
  const showPair = Boolean(client?.mintPairing && onOpenPair);
  const showDevices = Boolean(client?.listDevices);

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

  return (
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
  );
}

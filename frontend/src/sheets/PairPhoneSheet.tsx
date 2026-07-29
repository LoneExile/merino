import { useCallback, useEffect, useRef, useState } from "react";
import type { AccessOrigin, Client, PairingTicket } from "../client";
import { Sheet } from "../Sheet";

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

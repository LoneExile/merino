// The sign-in (OAuth) configuration block of the Access settings tab. Lives in
// both the menubar sheet (Wails transport) and the phone/browser dashboard
// (HTTP transport) — the Client interface hides which. Operator-only: the web
// transport only exposes client.oauthConfig for a non-device session, and the
// desktop transport is the Mac operator by definition.

import { useEffect, useState } from "react";
import type { Client, OAuthStatus } from "../../client";

interface Props {
  client: Client | null;
}

export function OAuthSettings({ client }: Props) {
  const [status, setStatus] = useState<OAuthStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  // GitHub form
  const [ghClientID, setGhClientID] = useState("");
  const [ghSecret, setGhSecret] = useState("");
  const [ghAllow, setGhAllow] = useState("");
  const [ghOrg, setGhOrg] = useState("");
  const [ghTeam, setGhTeam] = useState("");
  const [ghLabel, setGhLabel] = useState("");

  // OIDC form
  const [oiClientID, setOiClientID] = useState("");
  const [oiSecret, setOiSecret] = useState("");
  const [oiIssuer, setOiIssuer] = useState("");
  const [oiRole, setOiRole] = useState("");
  const [oiLabel, setOiLabel] = useState("");

  const [busy, setBusy] = useState(false);

  // Seed the forms from the server's current (secret-free) view. The secret
  // fields stay blank: an empty secret on save keeps the stored one.
  function apply(s: OAuthStatus) {
    setStatus(s);
    setGhClientID(s.github.clientID || "");
    setGhAllow((s.github.allow || []).join(", "));
    setGhOrg(s.github.org || "");
    setGhTeam(s.github.team || "");
    setGhLabel(s.github.label || "");
    setOiClientID(s.oidc.clientID || "");
    setOiIssuer(s.oidc.issuer || "");
    setOiRole(s.oidc.allowRole || "");
    setOiLabel(s.oidc.label || "");
    setGhSecret("");
    setOiSecret("");
  }

  useEffect(() => {
    if (!client?.oauthConfig) return;
    let alive = true;
    void client
      .oauthConfig()
      .then((s) => {
        if (alive) apply(s);
      })
      .catch((e) => {
        if (alive) setErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [client]);

  if (!client?.oauthConfig || !status) return null;

  const secretPlaceholder = (has: boolean) => (has ? "•••••••• (unchanged)" : "");

  function run(p: Promise<OAuthStatus>, ok: string) {
    setBusy(true);
    setErr(null);
    setMsg(null);
    void p
      .then((s) => {
        apply(s);
        setMsg(ok);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  }

  const gh = status.github;
  const oi = status.oidc;

  return (
    <section className="settings-block" aria-labelledby="set-sso">
      <header className="settings-block__head">
        <h3 id="set-sso">Single sign-on</h3>
      </header>
      <p className="settings-copy">
        Let people sign in to the dashboard with GitHub or Keycloak. Only the
        accounts you list below are admitted — credentials without an allowlist
        stay off.
      </p>

      {!status.publicUrlSet && (
        <p className="settings-copy settings-copy--warn">
          No public URL is set, so sign-in stays off until one is (OAuth needs an
          HTTPS origin for its redirect). You can still fill these in now.
        </p>
      )}

      {/* ---- GitHub ---- */}
      <h4 className="field__label">
        GitHub{" "}
        <span className="settings-row__hint">{gh.configured ? "· enabled" : "· off"}</span>
      </h4>
      {gh.envLocked ? (
        <p className="settings-copy settings-copy--quiet">
          Configured by environment variables — edit those to change it.
        </p>
      ) : (
        <>
          <label className="field__label" htmlFor="gh-cid">Client ID</label>
          <input id="gh-cid" className="field__input" value={ghClientID}
            autoCapitalize="off" autoCorrect="off" spellCheck={false}
            onChange={(e) => setGhClientID(e.target.value)} />
          <label className="field__label" htmlFor="gh-sec">Client secret</label>
          <input id="gh-sec" className="field__input" type="password" value={ghSecret}
            placeholder={secretPlaceholder(gh.hasSecret)} autoComplete="new-password"
            onChange={(e) => setGhSecret(e.target.value)} />
          <label className="field__label" htmlFor="gh-allow">Allowed logins (comma-separated)</label>
          <input id="gh-allow" className="field__input" value={ghAllow}
            autoCapitalize="off" autoCorrect="off" spellCheck={false} placeholder="octocat, hubot"
            onChange={(e) => setGhAllow(e.target.value)} />
          <label className="field__label" htmlFor="gh-org">Organization (optional)</label>
          <input id="gh-org" className="field__input" value={ghOrg}
            autoCapitalize="off" autoCorrect="off" spellCheck={false}
            onChange={(e) => setGhOrg(e.target.value)} />
          <label className="field__label" htmlFor="gh-team">Team (optional, needs org)</label>
          <input id="gh-team" className="field__input" value={ghTeam}
            autoCapitalize="off" autoCorrect="off" spellCheck={false}
            onChange={(e) => setGhTeam(e.target.value)} />
          <label className="field__label" htmlFor="gh-label">Button label (optional)</label>
          <input id="gh-label" className="field__input" value={ghLabel} placeholder="GitHub"
            onChange={(e) => setGhLabel(e.target.value)} />
          {gh.redirectURL && (
            <p className="settings-copy settings-copy--quiet">
              Callback URL to register at GitHub: <code>{gh.redirectURL}</code>
            </p>
          )}
          <div className="settings-actions">
            <button type="button" className="btn btn--solid" disabled={busy}
              onClick={() =>
                run(
                  client!.setOAuthGithub!({
                    clientID: ghClientID.trim(),
                    clientSecret: ghSecret,
                    allow: ghAllow.split(",").map((v) => v.trim()).filter(Boolean),
                    org: ghOrg.trim(),
                    team: ghTeam.trim(),
                    label: ghLabel.trim(),
                  }),
                  "GitHub sign-in saved.",
                )
              }>
              Save GitHub
            </button>
            {(gh.configured || gh.clientID) && (
              <button type="button" className="btn" disabled={busy}
                onClick={() => run(client!.clearOAuth!("github"), "GitHub sign-in removed.")}>
                Remove
              </button>
            )}
          </div>
        </>
      )}

      {/* ---- Keycloak / OIDC ---- */}
      <h4 className="field__label" style={{ marginTop: "1rem" }}>
        Keycloak (OIDC){" "}
        <span className="settings-row__hint">{oi.configured ? "· enabled" : "· off"}</span>
      </h4>
      {oi.envLocked ? (
        <p className="settings-copy settings-copy--quiet">
          Configured by environment variables — edit those to change it.
        </p>
      ) : (
        <>
          <label className="field__label" htmlFor="oi-cid">Client ID</label>
          <input id="oi-cid" className="field__input" value={oiClientID}
            autoCapitalize="off" autoCorrect="off" spellCheck={false}
            onChange={(e) => setOiClientID(e.target.value)} />
          <label className="field__label" htmlFor="oi-sec">Client secret</label>
          <input id="oi-sec" className="field__input" type="password" value={oiSecret}
            placeholder={secretPlaceholder(oi.hasSecret)} autoComplete="new-password"
            onChange={(e) => setOiSecret(e.target.value)} />
          <label className="field__label" htmlFor="oi-iss">Issuer URL</label>
          <input id="oi-iss" className="field__input" value={oiIssuer}
            autoCapitalize="off" autoCorrect="off" spellCheck={false}
            placeholder="https://kc.example/realms/myrealm"
            onChange={(e) => setOiIssuer(e.target.value)} />
          <label className="field__label" htmlFor="oi-role">Required realm/client role</label>
          <input id="oi-role" className="field__input" value={oiRole}
            autoCapitalize="off" autoCorrect="off" spellCheck={false} placeholder="herd-admin"
            onChange={(e) => setOiRole(e.target.value)} />
          <label className="field__label" htmlFor="oi-label">Button label (optional)</label>
          <input id="oi-label" className="field__input" value={oiLabel} placeholder="Keycloak"
            onChange={(e) => setOiLabel(e.target.value)} />
          {oi.redirectURL && (
            <p className="settings-copy settings-copy--quiet">
              Redirect URI to register at Keycloak: <code>{oi.redirectURL}</code>
            </p>
          )}
          <div className="settings-actions">
            <button type="button" className="btn btn--solid" disabled={busy}
              onClick={() =>
                run(
                  client!.setOAuthOidc!({
                    clientID: oiClientID.trim(),
                    clientSecret: oiSecret,
                    issuer: oiIssuer.trim(),
                    allowRole: oiRole.trim(),
                    label: oiLabel.trim(),
                  }),
                  "Keycloak sign-in saved.",
                )
              }>
              Save Keycloak
            </button>
            {(oi.configured || oi.clientID) && (
              <button type="button" className="btn" disabled={busy}
                onClick={() => run(client!.clearOAuth!("oidc"), "Keycloak sign-in removed.")}>
                Remove
              </button>
            )}
          </div>
        </>
      )}

      {err && <p className="composer__err" role="alert">{err}</p>}
      {msg && <p className="settings-copy">{msg}</p>}
    </section>
  );
}

// Extracted from the 1052-line SettingsSheet, where every tab's state lived in
// one scope and all five JSX trees were built on every render. Each tab now
// owns exactly the state it uses, so a reader can follow one without holding
// the other four.

import type { Client, Session } from "../../client";
import { displaySessionName } from "../names";

export interface AboutTabProps {
  client: Client | null;
  session: Session | null;
  isDesktop: boolean;
  onOpenSessions?: () => void;
}

export function AboutTab({ client, session, isDesktop, onOpenSessions }: AboutTabProps) {
  return (
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
  );
}

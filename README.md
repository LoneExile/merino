# herdr-tunnel

A menubar dashboard for [herdr](https://herdr.dev) coding agents. Go + [Wails v3](https://v3.wails.io) + React 19.

See which agents are working, which are blocked, and approve their prompts without leaving the menubar.

## Design

herdr exposes a socket API with a real push event stream, and this client is
built entirely on it. The `herdr` CLI is never invoked and nothing is polled:
one persistent unix socket carries every state change as it happens.

That buys immediate updates, zero subprocess churn, and a single static Go
binary with no runtime dependencies.

## Architecture

```
herdr server (unix socket, protocol 17)
  ├── lifecycle conn ── global subs, never restarted
  └── status conn ───── per-pane subs, re-dialled on pane-set change
            │
         Store (authoritative)
            ├── EmitEvent ──> React panel
            └── SetLabel ───> tray
```

Three decisions worth knowing:

**Go owns state; React is a pure projection.** The backend emits the whole
agent list on every change (coalesced, pre-sorted). React replaces its array
wholesale and never merges deltas.

**Two connections, deliberately.** A herdr subscription set is fixed when
`events.subscribe` is called — the connection *is* the subscription. Because
per-pane status subscriptions must change as panes come and go, that connection
gets re-dialled. Pane lifecycle lives on a separate, never-restarted connection
so churn can never cause a missed pane.

**Writes are allowlisted.** `pane.send_text` is unrestricted input to a live
terminal. Canned approvals, key names, pane IDs and text length are all
validated in `internal/app/guard.go` before anything reaches the socket.

## herdr protocol notes

Findings verified against a running herdr 0.7.5 (protocol 17). Several are not
documented and cost real debugging time — they are encoded in the types and
kept here so the next person doesn't rediscover them.

**Ordinary calls are one-shot.** The server closes the connection after
responding; a second request on the same connection fails with `EPIPE`. Dial
per call. `events.subscribe` is the exception and stays open.

**Status changes are invisible to global subscriptions.** `pane.updated` does
**not** fire on agent status transitions. Four forced transitions on a probe
pane produced *zero* `pane_updated` events for that pane while 58 fired for
unrelated panes. Status is only observable via per-pane
`pane.agent_status_changed`. Building on `pane.updated` yields an app that
never once reports a blocked agent.

**Two naming vocabularies.** Subscription *requests* use dotted names; delivered
*events* use snake_case for global subscriptions and dotted for per-pane ones.
Subscribing to `pane.updated` delivers events named `pane_updated`. Mixing them
gives a subscription that succeeds but whose events never match — a silent,
total failure.

**Closing a tab emits no `pane_closed`.** It destroys the tab's panes but only
emits `tab_closed`. Without handling that, panes leak in local state forever.

**Reported state ≠ observed status.** `pane.report_agent` accepts
`idle|working|blocked|unknown`; the stream reports
`idle|working|blocked|done|unknown`. Reporting `idle` is observed as `done`.
They are modelled as two distinct Go types for that reason.

**`BSpace` is rejected.** herdr answers `unsupported key BSpace` — use
`Backspace`. Also rejected: `^C`, `Home`, `End`, `PageUp`. Accepted: `Ctrl+c`,
`C-c`, `ctrl+c`, `esc`/`escape`/`Escape`, `Enter`, `Tab`, `Space`,
`Backspace`, arrows.

Because two undocumented gaps turned up in a single session, a low-frequency
(60s) reconcile against `pane.list` runs alongside the event stream. Events
remain the primary mechanism; the reconcile bounds the damage of the next gap
to one interval instead of forever.

## Development

Requires Go 1.25+, Node 22+, and a running herdr 0.7+.

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@latest   # or: see v3.wails.io
cd frontend && npm install && cd ..

wails3 dev            # live reload
wails3 build          # binary into bin/
wails3 package        # .app bundle
```

Regenerate the TypeScript bindings after changing any Go service signature:

```bash
wails3 generate bindings -d frontend/bindings -ts
```

### Tests

```bash
go test ./... -race
```

Tests in `internal/herdr` marked `TestLive*` exercise a real herdr server and
skip automatically when no socket is present.

### Layout

```
main.go                     app, tray, panel window
internal/herdr/             socket client — types, one-shot calls, event stream
internal/app/               store, subscription manager, guard, bound service
frontend/src/               React panel
frontend/bindings/          generated from Go (committed; do not hand-edit)
```

## Configuration

| Variable / flag | Purpose |
| --- | --- |
| `HERDR_SOCK` | Override the socket path (default `~/.config/herdr/herdr.sock`) |
| `HERDR_TUNNEL_DEBUG` | Enable debug logging |
| `--listen ADDR` | Serve the browser dashboard (e.g. `127.0.0.1:8730` or `0.0.0.0:8730`) |
| `HERDR_TUNNEL_USER` / `HERDR_TUNNEL_PASS` | Required whenever `--listen` is set |
| `--behind-proxy` | Trust `X-Forwarded-*` / CF headers; mark cookies Secure |
| `--allow-writes` | Let signed-in browsers approve / type / interrupt |
| `--allow-session-switch` | Let signed-in browsers repoint the herdr session |
| `HERDR_TUNNEL_PUBLIC_URL` | Public origin embedded in phone QR links (e.g. `https://herdr-tunnel.example`) |

Quick starts:

```bash
export HERDR_TUNNEL_PASS='…'
just web                     # loopback dashboard (127.0.0.1)
just web-lan                 # LAN bind 0.0.0.0 (HTTP)
just tunnel                  # loopback + --behind-proxy for a TLS tunnel
```

### Phone sign-in (QR)

With `--listen` running and a public URL (Cloudflare tunnel, etc.), open
**Settings → Phone sign-in** in the menu-bar panel. Mint a QR; scan it on the
phone. The code is single-use and expires in two minutes. If you cannot scan,
paste the short code into the **Phone code** field on `/login`.

Desktop Settings also exposes **Launch at login** and **Check for updates**
(GitHub Releases). Tag a release (`v*`) to trigger `.github/workflows/release.yml`.

## Status

Working: live agent list grouped by workspace, blocked-first ordering, tray
label with counts, approvals, interrupt, focus, live pane output, browser
dashboard (password + QR pairing), Web Push, launch-at-login, GitHub update
check, tagged macOS release packaging.

Not built yet: Telegram bot, multi-machine over SSH.

## License

Apache-2.0 — see [LICENSE](LICENSE).

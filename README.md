# Merino

**Merino** is the menu-bar and phone control surface for [herdr](https://herdr.dev) coding agents.

See which agents are working, which are blocked, and approve their prompts without leaving the menubar — or from a phone on the same network / Tailscale / HTTPS tunnel.

> Sheep mark, quiet instrument panel. Built on herdr\'s socket API.

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

## Quick start

```bash
just app          # build + launch the menu-bar app (Merino)
export MERINO_PASS='…'
just web          # loopback dashboard
just tunnel       # LAN + --behind-proxy for a TLS tunnel
```

Legacy env names `HERDR_TUNNEL_*` still work for one release.

## Configuration

| Flag / env | Purpose |
| --- | --- |
| `--listen ADDR` | Serve the web dashboard (e.g. `127.0.0.1:8730` or `0.0.0.0:8730`) |
| `--allow-writes` | Let the dashboard type into terminals (audit-logged) |
| `--allow-session-switch` | Let the dashboard change herdr session |
| `--behind-proxy` | Secure cookies + trust proxy headers (Cloudflare etc.) |
| `MERINO_DEBUG` | Enable debug logging |
| `MERINO_USER` / `MERINO_PASS` | Operator credentials when listening |
| `MERINO_PUBLIC_URL` | Public origin embedded in phone QR links |
| `MERINO_OIDC_*` | Optional OAuth scaffold |
| `HERDR_SOCK` | herdr socket path |

State and logs live under `~/Library/Logs/merino/` (macOS).

## Brand

**Merino** is the product name. The mark is a sheep (same family as herdr).
Repo and module: `github.com/LoneExile/merino`.

## License

See repository license file.

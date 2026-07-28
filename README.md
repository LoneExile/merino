# Merino

<p align="center">
  <img src="build/appicon.png" alt="Merino" width="96" height="96" />
</p>

<p align="center">
  <strong>Menu bar + phone dashboard for <a href="https://herdr.dev">herdr</a> agents</strong><br/>
  See what’s working, what’s blocked, and answer prompts without leaving the flock.
</p>

<p align="center">
  <a href="https://github.com/LoneExile/merino/actions/workflows/ci.yml"><img src="https://github.com/LoneExile/merino/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/LoneExile/merino/releases/latest"><img src="https://img.shields.io/github/v/release/LoneExile/merino?display_name=tag&sort=semver" alt="Release" /></a>
  <a href="https://github.com/LoneExile/merino/blob/main/LICENSE"><img src="https://img.shields.io/github/license/LoneExile/merino" alt="License" /></a>
  <a href="https://github.com/LoneExile/merino/stargazers"><img src="https://img.shields.io/github/stars/LoneExile/merino?style=social" alt="Stars" /></a>
  <img src="https://img.shields.io/badge/platform-macOS-black" alt="macOS" />
  <img src="https://img.shields.io/badge/built_with-Go_%2B_Wails_%2B_React-00ADD8" alt="Stack" />
</p>

---

## Why Merino

[herdr](https://herdr.dev) runs coding agents in terminal panes. Merino is the **instrument panel**:

- Live agent list (blocked first)
- Pane output streaming
- Approve / type / interrupt when you enable writes
- Phone PWA over LAN, Tailscale, or a public HTTPS tunnel
- One-shot QR pairing with revocable device grants

No CLI polling. No `herdr` subprocess. One persistent socket, push events only.

## Install

### Homebrew (recommended)

```bash
brew tap LoneExile/merino
brew install --cask merino
```

Upgrade later:

```bash
brew update && brew upgrade --cask merino
```

### GitHub Releases

1. Download the latest **macOS** zip from [Releases](https://github.com/LoneExile/merino/releases/latest)
2. Open `merino.app` (right-click → Open the first time if Gatekeeper prompts)
3. Look for the sheep in the menu bar

### Build from source

```bash
git clone https://github.com/LoneExile/merino.git
cd merino
just app          # builds Merino.app and launches it
```

Requirements: Go (see `go.mod`), Node 22+, macOS, [Wails v3](https://v3.wails.io) for packaging.

## Quick start

1. Install and open **Merino** (menu bar sheep).
2. Right-click the tray icon → **Pair phone…**
3. Scan the QR (or paste the code) on your phone.
4. Optional: Settings → allow session switch / password sign-in.

Phone dashboard defaults to read-only. Enable writes only if you want the phone to type into live terminals (all writes are audit-logged).

## Configuration

| Flag / env | Purpose |
| --- | --- |
| `--listen ADDR` | Web dashboard bind (e.g. `0.0.0.0:8730`) |
| `--allow-writes` | Approve prompts, keys, interrupts (audit-logged) |
| `--allow-session-switch` | Change which herdr session the dashboard drives |
| `--behind-proxy` | Secure cookies + trust proxy headers (TLS terminator) |
| `MERINO_USER` / `MERINO_PASS` | HTTP operator credentials when listening |
| `MERINO_PUBLIC_URL` | Public origin for QR links (HTTPS recommended) |
| `MERINO_DEBUG` | Debug logging |
| `HERDR_SOCK` | herdr socket path |

State lives under `~/Library/Logs/merino/` on macOS.

```bash
export MERINO_PASS='…'
just web          # loopback dashboard
just tunnel       # LAN bind + --behind-proxy
```

## Architecture

```
herdr server (unix socket)
  ├── lifecycle connection  — global pane events
  └── status connection     — per-pane agent status
            │
         Store (Go, authoritative)
            ├── events ──▶ React panel / phone PWA
            └── tray label + sheep animation
```

**Go owns state; React projects it.** The backend emits a full sorted agent list on each change. The UI never merges deltas.

**Writes are allowlisted** in `internal/app/guard.go` before anything reaches a live terminal.

## Development

```bash
just              # list recipes
just app          # package + launch menu bar app
just gate         # local CI-ish checks
just web          # needs MERINO_PASS
```

## Brand

**Merino** — sheep mark, quiet instrument panel. Built for herdr; not a fork of herdr.

## License

[Apache-2.0](LICENSE)

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
</p>

---

## Why Merino

[herdr](https://herdr.dev) runs coding agents in terminal panes. Merino is the **instrument panel**:

- Live agent list, blocked first
- Streaming pane output, in colour
- Approve, type, and interrupt when you enable writes
- Start a new agent in any workspace, without going back to the Mac
- Phone dashboard over LAN, Tailscale, or a Cloudflare Tunnel
- Installs to your phone's home screen as a PWA, with push when an agent blocks
- One-shot QR pairing with revocable, per-device access

## Screenshots

<table>
  <tr>
    <td align="center" width="50%">
      <img src="assets/screenshot/agent-list.png" alt="Menubar agent list" width="420" /><br />
      <sub><b>Menubar</b> — live agent list, blocked first</sub>
    </td>
    <td align="center" width="50%">
      <img src="assets/screenshot/pair-phone.png" alt="Pair phone QR" width="420" /><br />
      <sub><b>Pair phone</b> — QR + one-shot code, Mac / LAN / Tailscale</sub>
    </td>
  </tr>
  <tr>
    <td align="center" width="50%">
      <img src="assets/screenshot/phone-pane.jpg" alt="Phone pane with inline image" width="420" /><br />
      <sub><b>Phone pane</b> — stream + inline images like Kitty</sub>
    </td>
    <td align="center" width="50%">
      <img src="assets/screenshot/phone-slash.jpg" alt="Phone slash command typeahead" width="420" /><br />
      <sub><b>Composer</b> — slash commands, attach image, send</sub>
    </td>
  </tr>
</table>

## Requirements

- macOS on **Apple Silicon** (prebuilt binaries; Intel needs a source build)
- [herdr](https://herdr.dev) running — Merino is a view onto it, not a replacement

## Install

Builds are **ad-hoc signed** (no Apple Developer ID or notarization yet), so every path below clears the Gatekeeper quarantine bit for you.

### One-liner

```bash
curl -fsSL https://raw.githubusercontent.com/LoneExile/merino/main/scripts/install.sh | bash
```

Downloads the latest [release](https://github.com/LoneExile/merino/releases/latest), installs **Merino.app** into `/Applications`, strips quarantine, and opens it. Override the location with `MERINO_APP_DIR=~/Applications`.

### Homebrew

```bash
brew tap LoneExile/merino
brew trust LoneExile/merino          # third-party tap
brew install --cask merino
```

Upgrade with `brew update && brew upgrade --cask merino`.

No `--no-quarantine` needed, and Homebrew 6 rejects it outright: the cask's own
postflight strips the quarantine bit after install.

### Manual

1. Download **Merino-\*-macos-arm64.zip** from [Releases](https://github.com/LoneExile/merino/releases/latest)
2. Unzip and drag **merino.app** into `/Applications`
3. Clear quarantine and open:

```bash
xattr -dr com.apple.quarantine /Applications/Merino.app 2>/dev/null || \
  xattr -dr com.apple.quarantine /Applications/merino.app
open /Applications/Merino.app 2>/dev/null || open /Applications/merino.app
```

If macOS still says *“Apple could not verify…”*, use **System Settings → Privacy & Security → Open Anyway**, or right-click the app and choose **Open**.

## Quick start

1. Open **Merino**. A sheep appears in your menu bar.
2. **Left-click** it for the panel; **right-click** for the menu.
3. Right-click → **Pair phone…**, then scan the QR on your phone.

The sheep reports the herd without you looking at it: it **jumps** when an agent is blocked, **walks** when one is working, and stands still when nothing needs you.

### Start a new agent

Click **+** in the panel (or right-click → **New agent…**), pick a workspace and an agent, and it opens as soon as it is ready. Only agents actually installed on your Mac are offered.

## Signing in from a phone

**QR pairing is the way in.** Each phone gets its own grant, which you can revoke without touching anything else.

Username/password sign-in is **off by default** — a password that a whole LAN, or the public internet, can attempt is the weakest door this app has. Turn it on deliberately in **Settings → Access** if you need it (for example to sign in from a browser you cannot pair with a QR).

Lost a phone? **Settings → Pairing → Panic revoke all phones**.

**How long you stay signed in.** A paired phone stays signed in while you keep
using it, and is signed out after **12 hours idle** or **7 days** since
pairing, whichever comes first. Quitting Merino signs every device out
immediately — the keys that sign sessions live in memory only, so a stolen
session cannot outlive the app. After any of those, scan a new QR.

## Settings

Everything is in the panel under **Settings**, grouped by intent:

| Tab | What lives there |
| --- | --- |
| **Pairing** | Show the pairing QR, see paired devices, revoke one or all |
| **Access** | Phone writes, session switch, password sign-in, phone password |
| **Display** | Theme, line wrap, terminal font size |
| **System** | Launch at login, updates, notification alerts |
| **About** | Who you are signed in as, transport, keyboard shortcuts |

**Phone writes** decides whether a paired phone can answer asks, type, and interrupt agents. It is on by default in the menu bar app. Every write is recorded in the audit log at `~/Library/Logs/merino/audit.jsonl`. Reload the phone dashboard after changing it.

## Install it on your phone

The dashboard is a PWA. Adding it to the home screen gets you a standalone
window with no browser chrome, the Merino icon in your app grid, and on iPhone
it is the only way to receive push notifications.

- **iPhone / iPad**: Safari, then **Share → Add to Home Screen**. Safari tabs
  never receive push, so for alerts this is required rather than cosmetic.
- **Android**: Chrome offers **Install app** in the address bar or the ⋮ menu.

It is not an offline app, and does not pretend to be. The service worker caches
the app shell so it opens instantly and rides out a flaky connection, but every
agent, every line of pane output and every keystroke comes from your Mac live.
With the Mac asleep you get the shell and an empty herd.

## Notifications

Enable them in **Settings → System** to be told the moment an agent needs you, even with the app closed.

On iPhone this requires [installing the dashboard to your Home Screen](#install-it-on-your-phone) first. Safari tabs do not receive push.

## Reaching it from your phone

Merino serves the dashboard on port **8730** and listens on your whole network
by default, so nothing needs configuring for the two common cases:

- **Same Wi‑Fi** — pair with the QR and you are done.
- **Tailscale** — the pairing sheet lists your Tailscale address alongside the
  LAN one. Pick that and the phone works from anywhere on your tailnet.

Both are in **Pair phone…**, which shows every address your Mac can be reached
at and mints a one-shot code for the one you choose.

Do not simply forward port 8730 from your router. It is plain HTTP, and the
dashboard can type into live terminals. For access from outside your network,
use a tunnel.

Your data (audit log, device grants, keys) stays under
`~/Library/Logs/merino/`.

### Cloudflare Tunnel

A [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
gives you real HTTPS on a hostname you own, with no inbound port open anywhere:
the connector dials out to Cloudflare, so your router keeps saying no to
everything.

This is the one part of Merino that needs the command line. A bundle launched
from Finder, Homebrew or login items is given no arguments and inherits no
shell environment, so neither setting below can reach the installed app.

| Setting | Why |
| --- | --- |
| `--behind-proxy` | Marks session cookies Secure, and trusts the proxy's client-IP header so the login throttle counts the phone rather than the tunnel. |
| `MERINO_PUBLIC_URL` | Points the pairing QR at the public origin. Without it Merino advertises the LAN address it can see, which your phone cannot open from outside. |

Where to bind depends on where `cloudflared` runs.

**Connector on the same Mac**: bind loopback, and nothing else can reach the
origin at all.

```bash
MERINO_PUBLIC_URL=https://merino.example.com \
  merino --listen 127.0.0.1:8730 --behind-proxy
```

**Connector in Docker or a VM**: it cannot reach the host's loopback, so bind
the LAN interface.

```bash
MERINO_PUBLIC_URL=https://merino.example.com \
  merino --listen 0.0.0.0:8730 --behind-proxy
```

The second shape has a trade-off worth stating: with a LAN bind, another
machine on your network can spoof `X-Forwarded-For` or `CF-Connecting-IP` and
dilute the login throttle. That is acceptable on a home LAN behind a firewall.
It is not a reason to let port 8730 past that firewall.

Then point the tunnel's public hostname at whichever origin you chose. The
`tunnel` and `tunnel-loopback` recipes in the justfile wrap both shapes for
development; see [CONTRIBUTING.md](CONTRIBUTING.md).

## Troubleshooting

**The menu bar panel opens in the middle of the screen.** Fixed in current builds; update.

**The phone shows “read-only”.** Turn on **Settings → Access → Allow phone writes**, then reload the phone dashboard.

**The login page says password sign-in is disabled.** That is the default. Pair with a QR, or enable it in **Settings → Access**.

**The public URL returns `530` but the Mac app is running.** No connector is registered, so Cloudflare fails before the request ever reaches your Mac. The problem is the tunnel, not Merino: check the connector is running. If it lives in Docker, check the container is up.

**The public URL returns `502`.** The opposite: the connector is up and Cloudflare reached it, but it could not reach Merino. Almost always the origin bind. A connector in Docker cannot reach the Mac's loopback, so `--listen 127.0.0.1:8730` is unreachable to it; use `0.0.0.0:8730`. See [Cloudflare Tunnel](#cloudflare-tunnel).

**Signed in over a tunnel, but the QR still shows a LAN address.** `MERINO_PUBLIC_URL` is unset, so Merino is advertising the address it can see rather than the one your phone uses.

**An agent is missing from the New agent list.** Merino offers only agents it can find in your login shell. If `command -v <agent>` works in a normal terminal but the agent is missing here, reopen Settings — the list is cached briefly.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/LoneExile/merino/main/scripts/uninstall.sh | bash
```

Add `-s -- --keep-state` to keep paired devices and settings, or `-s -- --dry-run` to preview. Manually:

```bash
osascript -e 'quit app "Merino"' 2>/dev/null || true
rm -rf /Applications/Merino.app /Applications/merino.app   # or: brew uninstall --cask merino
rm -rf ~/Library/Logs/merino ~/Library/Caches/merino
rm -f ~/Library/Preferences/dev.apinant.merino.plist
```

This does not remove [herdr](https://herdr.dev).

## Contributing

Build instructions, architecture, and the development loop are in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[Apache-2.0](LICENSE)

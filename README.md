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
- Phone dashboard over LAN, Tailscale, or a public HTTPS tunnel
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
brew install --cask --no-quarantine merino
```

Upgrade with `brew update && brew upgrade --cask --no-quarantine merino`.

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

## Notifications

Enable them in **Settings → System** to be told the moment an agent needs you, even with the app closed.

On iPhone this requires installing the dashboard to your Home Screen first (**Share → Add to Home Screen**). Safari tabs do not receive push.

## Reaching it from outside your network

Merino serves the dashboard on port **8730**. Over anything other than your own LAN, put it behind TLS — a Cloudflare tunnel or Tailscale, not a forwarded port.

If you run Merino behind a TLS terminator, start it with `--behind-proxy` so session cookies are marked secure and the login throttle counts the real client rather than the proxy.

| Setting | Purpose |
| --- | --- |
| `--listen ADDR` | Dashboard bind address (default `0.0.0.0:8730`) |
| `--behind-proxy` | Secure cookies, trust proxy client-IP headers |
| `--allow-writes` | Force phone writes on at startup |
| `--allow-session-switch` | Let the dashboard change which herdr session it drives |
| `MERINO_PUBLIC_URL` | Public origin baked into pairing QR links |
| `MERINO_DEBUG=1` | Verbose logging |
| `HERDR_SOCK` | herdr socket path, if not the default |

Your data — audit log, device grants, keys — stays under `~/Library/Logs/merino/`.

## Troubleshooting

**The menu bar panel opens in the middle of the screen.** Fixed in current builds; update.

**The phone shows “read-only”.** Turn on **Settings → Access → Allow phone writes**, then reload the phone dashboard.

**The login page says password sign-in is disabled.** That is the default. Pair with a QR, or enable it in **Settings → Access**.

**The public URL returns an error but the Mac app is running.** The problem is almost certainly your tunnel, not Merino. Check that your tunnel connector is up — Cloudflare answers `530` when no connector is registered, before the request ever reaches your Mac.

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

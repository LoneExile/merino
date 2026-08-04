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
  <a href="https://github.com/sponsors/LoneExile"><img src="https://img.shields.io/github/sponsors/LoneExile?style=social" alt="Sponsors" /></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-black" alt="macOS and Linux" />
</p>

---

[herdr](https://herdr.dev) runs coding agents in terminal panes. Merino is the
instrument panel: a live agent list with blocked ones first, streaming pane
output in colour, and a phone dashboard so you can answer an agent from
wherever you are.

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

## Install

Needs [herdr](https://herdr.dev) already running.

**macOS** — the menu bar app, on Apple Silicon.

```bash
curl -fsSL https://raw.githubusercontent.com/LoneExile/merino/main/scripts/install.sh | bash
```

**Linux** — [`merinod`](#headless), the same dashboard with no menu bar, run
as a background service.

```bash
curl -fsSL https://raw.githubusercontent.com/LoneExile/merino/main/scripts/install-merinod.sh | bash
```

Both verify what they download and put it somewhere sensible —
`/Applications` and `/usr/local/bin` respectively, overridable with
`MERINO_APP_DIR` and `MERINOD_BIN_DIR`. macOS builds are ad-hoc signed (no
Developer ID, no notarization), so the installer clears the quarantine bit
for you.

<details>
<summary>Other ways in</summary>

**Homebrew** (macOS)

```bash
brew tap LoneExile/merino
brew trust LoneExile/merino          # third-party tap
brew install --cask merino
```

Upgrade with `brew update && brew upgrade --cask merino`. No
`--no-quarantine` needed — the cask's postflight strips it, and Homebrew 6
rejects the flag outright.

**Manual** (macOS) — grab `Merino-*-macos-arm64.zip` from
[Releases](https://github.com/LoneExile/merino/releases/latest), drag it to
`/Applications`, then:

```bash
xattr -dr com.apple.quarantine /Applications/Merino.app
open /Applications/Merino.app
```

If macOS still refuses, right-click → **Open**, or
**System Settings → Privacy & Security → Open Anyway**.

**Manual** (Linux) — `merinod-linux-{amd64,arm64}` from the same releases,
verified against `SHA256SUMS`.

**From source** — `go build ./cmd/merinod` for the daemon; Intel Macs need a
source build of the app, see [CONTRIBUTING.md](CONTRIBUTING.md).

</details>

## Quick start

1. Open Merino. A sheep appears in your menu bar.
2. Left-click for the panel, right-click for the menu.
3. Right-click → **Pair phone…** and scan the QR.

The sheep reports the herd without you looking: it **jumps** when an agent is
blocked, **walks** when one is working, stands still when nothing needs you.

**+** in the panel starts a new agent in any workspace — only agents actually
installed on your Mac are offered.

## From your phone

Same Wi-Fi or Tailscale works with no configuration; **Pair phone…** lists
every address your Mac answers on. For anything outside your network, use a
tunnel — **do not forward port 8730**, it is plain HTTP and the dashboard can
type into live terminals.

Add it to your home screen for push notifications. On iPhone that is the only
way to get them.

→ [Phone access](docs/phone-access.md): Cloudflare Tunnel, pairing and session
lifetime, PWA install.

## Settings

| Tab | |
| --- | --- |
| **Pairing** | Pairing QR, paired devices, revoke one or all |
| **Access** | Phone writes, session switch, password sign-in |
| **Display** | Theme, line wrap, terminal font size |
| **System** | Launch at login, updates, notification alerts |
| **About** | Identity, transport, keyboard shortcuts |

**Phone writes** decides whether a paired phone can answer asks, type and
interrupt. On by default in the menu bar app, and every write lands in
`~/Library/Logs/merino/audit.jsonl`. Reload the phone after changing it.

## Headless

`merinod` is the same dashboard with no menu bar — a static Linux binary for
systemd, Docker or Kubernetes. Install it with the script
[above](#install), grab `merinod-linux-{amd64,arm64}` from
[Releases](https://github.com/LoneExile/merino/releases/latest), or build it:
`go build ./cmd/merinod`.

Releases also publish a multi-arch container image,
`ghcr.io/loneexile/merinod`, for linux/amd64 and linux/arm64. Pin a version:
`latest` follows stable releases only, so a pre-release never moves it.

Pair a phone from the dashboard's **Settings → Pairing**, or from a shell —
`merinod qr` draws the code in the terminal, which over SSH beats finding a
browser:

```
ssh myserver merinod qr
```

Two things decide every deployment: herdr has no network port, only a unix
socket, so merinod opens a *file* rather than connecting to anything; and it
must run as the **user that owns that socket**, or the agent list stays empty
while everything else looks healthy.

→ [deploy/](deploy/): systemd, Docker and Kubernetes, with manifests.

## More

- [Troubleshooting](docs/troubleshooting.md)
- [Contributing](CONTRIBUTING.md) — build, architecture, dev loop
- [Changelog](CHANGELOG.md)

<details>
<summary>Uninstall</summary>

```bash
curl -fsSL https://raw.githubusercontent.com/LoneExile/merino/main/scripts/uninstall.sh | bash
```

`-s -- --keep-state` keeps paired devices and settings; `-s -- --dry-run`
previews. By hand:

```bash
osascript -e 'quit app "Merino"' 2>/dev/null || true
rm -rf /Applications/Merino.app          # or: brew uninstall --cask merino
rm -rf ~/Library/Logs/merino ~/Library/Caches/merino
rm -f  ~/Library/Preferences/dev.apinant.merino.plist
```

Leaves [herdr](https://herdr.dev) alone.

</details>

## Sponsor

Merino is free and open source. If the sheep keeps your agents out of
trouble, [sponsor the work](https://github.com/sponsors/LoneExile) on GitHub
Sponsors.

## License

[Apache-2.0](LICENSE)

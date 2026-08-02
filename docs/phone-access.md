# Phone access

Merino serves the dashboard on port **8730**, bound to `0.0.0.0` by default.
Same Wi-Fi and Tailscale need no configuration; anything from outside your
network needs a tunnel.

**Do not forward port 8730 from your router.** It is plain HTTP and the
dashboard can type into live terminals.

## Getting on

**Pair phone…** lists every address your Mac answers on and mints a one-shot
code for the one you pick.

| From | Address to pick |
| --- | --- |
| Same Wi-Fi | the LAN address |
| Tailnet | the Tailscale address |
| Anywhere else | your tunnel hostname — see below |

## Pairing and sessions

QR pairing is the way in. Each phone gets its own grant, revocable
independently.

Username/password sign-in is **off by default** — a password the whole LAN or
the public internet can attempt is the weakest door here. **Settings → Access**
if you want it, e.g. for a browser you cannot pair by QR.

| | |
| --- | --- |
| Idle timeout | 12 hours |
| Absolute cap | 7 days from pairing |
| On quit | every device signed out immediately |

Session-signing keys live in memory only, so a stolen session cannot outlive
the app. Lost a phone: **Settings → Pairing → Panic revoke all phones**.

## Home screen

The dashboard is a PWA. Installing it gets a standalone window and, on iPhone,
is the **only** way to receive push — Safari tabs never do.

- **iOS**: Safari → Share → Add to Home Screen
- **Android**: Chrome → Install app

Then enable alerts in **Settings → System**.

The service worker caches the app shell only. Every agent, every line of
output and every keystroke is live from your Mac; with the Mac asleep you get
the shell and an empty herd.

## Cloudflare Tunnel

Real HTTPS on a hostname you own, no inbound port anywhere — the connector
dials out.

This is the one part of Merino that needs the command line: a bundle launched
from Finder, Homebrew or login items gets no arguments and inherits no shell
environment, so neither setting below can reach the installed app.

| Setting | Why |
| --- | --- |
| `--behind-proxy` | Marks cookies Secure; trusts the proxy's client-IP header so the login throttle counts the phone, not the tunnel. |
| `MERINO_PUBLIC_URL` | Points the pairing QR at the public origin. Without it Merino advertises the LAN address it can see. |

Bind depends on where `cloudflared` runs.

**Same Mac** — bind loopback and nothing else can reach the origin:

```bash
MERINO_PUBLIC_URL=https://merino.example.com \
  merino --listen 127.0.0.1:8730 --behind-proxy
```

**Docker or a VM** — it cannot reach the host's loopback, so bind the LAN
interface:

```bash
MERINO_PUBLIC_URL=https://merino.example.com \
  merino --listen 0.0.0.0:8730 --behind-proxy
```

The second shape has a trade-off: with a LAN bind, another machine on your
network can spoof `X-Forwarded-For` or `CF-Connecting-IP` and dilute the login
throttle. Acceptable on a home LAN behind a firewall. Not a reason to let 8730
past that firewall.

Then point the tunnel's hostname at whichever origin you chose. The `tunnel`
and `tunnel-loopback` recipes in the justfile wrap both shapes for development
— see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Where state lives

`~/Library/Logs/merino/` — audit log, device grants, keys.

# Troubleshooting

## Menu bar app

**Panel opens in the middle of the screen.** Fixed in current builds; update.

**An agent is missing from the New agent list.** Merino offers only agents it
finds in your login shell. If `command -v <agent>` works in a normal terminal,
reopen Settings — the list is cached briefly.

**The phone shows "read-only".** **Settings → Access → Allow phone writes**,
then reload the phone dashboard.

**The login page says password sign-in is disabled.** That is the default.
Pair with a QR, or enable it in **Settings → Access**.

## Tunnels

**`530` from the public URL, app running.** No connector registered —
Cloudflare fails before reaching your Mac. Check the connector is up; if it
lives in Docker, check the container.

**`502` from the public URL.** The connector is up but could not reach Merino.
Almost always the origin bind: a connector in Docker cannot reach the Mac's
loopback, so `--listen 127.0.0.1:8730` is unreachable to it. Use
`0.0.0.0:8730`. See [phone access](phone-access.md#cloudflare-tunnel).

**Signed in over a tunnel, QR still shows a LAN address.**
`MERINO_PUBLIC_URL` is unset.

## Headless (`merinod`)

**Agent list empty, everything else healthy.** Almost always socket ownership:

```
dial herdr socket ...: connect: permission denied
```

merinod must run as the user that owns `herdr.sock`. `/healthz` returns 200
throughout, because it describes the merinod process, not the herd.

More, including Kubernetes-specific failures, in [deploy/](../deploy/#when-it-does-not-work).

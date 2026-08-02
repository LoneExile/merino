# Running `merinod`

`merinod` is Merino without the menu bar: the same dashboard, the same login
wall, the same phone UI, run as a background service on Linux.

Everything here assumes one rule. **herdr has no network port.** It listens on
a unix socket — `~/.config/herdr/herdr.sock` — and nothing else. So merinod
does not connect to herdr over a network; it opens a file. Every layout below
is a different way of putting that file within reach.

And a second rule that follows from the first: a socket is a file with an
owner, and herdr's is `srw-------`. **merinod has to run as the user that owns
it.** Get this wrong and nothing looks broken — the dashboard serves, sign-in
works, `/healthz` says 200 — but the agent list stays empty forever.

---

## Which layout do you want?

| Your situation | Use |
| --- | --- |
| herdr runs on a Linux box, and merinod can too | [systemd](#systemd) |
| herdr runs on a Linux box, you want merinod in a container | [Docker](#docker) |
| herdr runs somewhere else (a laptop, a workstation, behind NAT) and merinod runs in Kubernetes | [Kubernetes](#kubernetes) |

Prefer systemd whenever it is available. It is the only layout where paste,
session discovery and spawning all work without qualification, because herdr
and merinod share a machine and a user.

Get the binary first. Releases carry `merinod-linux-amd64` and
`merinod-linux-arm64`, and the installer picks the right one, verifies it
against the release checksums, and drops it in your PATH:

```bash
curl -fsSL https://raw.githubusercontent.com/LoneExile/merino/main/scripts/install-merinod.sh | bash
```

`MERINOD_BIN_DIR` and `MERINOD_VERSION` override where and which. Building it
yourself works too — `go build ./cmd/merinod`.

For Kubernetes you need a container image. Releases publish one as
`ghcr.io/loneexile/merinod:<tag>`, multi-arch for linux/amd64 and
linux/arm64, pullable without credentials:

```bash
docker pull ghcr.io/loneexile/merinod:v0.3.0
```

The manifests here are pinned to that tag. Pin whatever you use: `latest`
follows stable releases and will shift under an unpinned redeploy, and a
pre-release publishes only its own version without ever moving it.

To own the artefact instead, build and push your own:

```bash
docker build -f build/docker/Dockerfile.merinod -t <registry>/merinod:v1 .
docker push <registry>/merinod:v1
```

---

## systemd

Runs merinod as your own user, next to herdr.

```bash
# merinod already on PATH from the installer above; otherwise:
#   go build -o ~/.local/bin/merinod ./cmd/merinod
merinod config init                       # writes ~/.config/merino/config.yml
$EDITOR ~/.config/merino/config.yml       # at minimum, see "Signing in" below

mkdir -p ~/.config/systemd/user
cp build/systemd/merino.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now merino
systemctl --user status merino
```

A `--user` unit, not a system one, because the socket lives in your home
directory and belongs to you.

To survive logout, enable lingering: `sudo loginctl enable-linger $USER`.

---

## Docker

herdr stays on the host; merinod runs in a container that mounts herdr's
config directory.

```bash
docker compose -f deploy/compose.yaml up -d
```

Two things to set before that works:

**Run as the socket's owner.** The image runs as uid 65532. Check who owns the
socket and match it:

```bash
ls -l ~/.config/herdr/herdr.sock      # -> srw------- 1 you you
```

then uncomment `user: "${UID}:${GID}"` in `compose.yaml`.

**Mount the whole directory, not the socket file.** `compose.yaml` already
does. herdr replaces the socket file whenever it restarts, and a bind mount of
the old file keeps pointing at something that no longer exists.

---

## Kubernetes

herdr stays wherever it is. An `autossh` sidecar carries its socket into the
pod over SSH, and merinod opens the forwarded socket.

This is the only layout that reaches a herd behind NAT, and the SSH key is
what authenticates it.

```bash
# 1. Build and push an image your cluster can pull.
docker build -f build/docker/Dockerfile.merinod -t <registry>/merinod:v1 .
docker push <registry>/merinod:v1

# 2. A key for the tunnel, restricted on the herd host.
ssh-keygen -t ed25519 -N '' -f ./merino-forward -C merino-forward
ssh-keyscan -t ed25519 <herd-host> > ./known_hosts

# On the herd host, prefix the public key in ~/.ssh/authorized_keys with:
#   command="/bin/false",no-pty,no-agent-forwarding,no-X11-forwarding,no-user-rc
# That refuses any shell while still allowing the forward.

kubectl create secret generic merino-ssh-key \
  --from-file=id_ed25519=./merino-forward \
  --from-file=known_hosts=./known_hosts
kubectl create secret generic merino-dashboard-password --from-literal=password='...'

# 3. Fill in every REPLACE-ME in the manifest, then apply.
kubectl apply -f deploy/k8s/merino-ssh.yaml
```

`grep REPLACE-ME deploy/k8s/merino-ssh.yaml` lists what you must set: the
image, the StorageClass, and the herd's SSH host and user.

### What does not work in this layout

- **Image paste.** Merino resolves pasted image paths under its own `$HOME`;
  the agent reads them on the herd's host. Different filesystems.
- **Session switching.** Merino lists sessions from its own
  `~/.config/herdr/sessions/`, which in the pod is empty. Leave
  `access.allowSessionSwitch: false`.
- **A sleeping herd host.** The tunnel needs the far end awake. A closed
  laptop is a blank dashboard.

### The alternative: herdr in the pod

`k8s/merino-pod.yaml` runs `herdr server` and merinod in one pod, sharing the
socket through an `emptyDir` — no SSH, nothing to keep awake. It needs a herdr
image with your agent CLIs installed and credentialed, and no such image
exists yet, so it ships as a documented starting point rather than a
supported path.

---

## Signing in

You need a first sign-in before you can pair anything, because minting a QR
is an operator action and a phone is not an operator. Name a password file:

```yaml
access:
  passwordLogin: true
auth:
  user: "operator"
  passwordFile: "/run/secrets/merino-password"   # a Kubernetes Secret is a file
```

The password is read from the file. There is no `--password` flag and no
password key in the config: argv is world-readable and a literal in
`config.yml` travels with the deployment.

### Pairing a phone

Sign in with that password, then **Settings → Pairing → Show pair QR**. The
dashboard mints a one-shot ticket and the server renders the QR, so this
works with no menu bar and no desktop — the same flow the Mac app uses,
driven from the browser.

`publicUrl` must be right before you scan anything: the QR encodes it, and a
container that guesses gets its own bridge address, which no phone can open.

Paired phones cannot mint. The server refuses `/api/pairing/mint` from a
device session, so a lost phone cannot issue itself a replacement — revoking
it under **Paired devices** is final.

## Two settings people miss

**`publicUrl`** — required anywhere merinod cannot see the address your phone
will use. Left empty it guesses from its own network interfaces, which inside
a container is the container's own address: a URL nothing can open.

**`paths.stateDir`** — must be a volume. It holds paired devices, the Web Push
keypair and push subscriptions. Losing it signs out every phone, and push then
fails *silently*: each browser holds a subscription signed by a key you no
longer have, and nothing reports an error.

## One dashboard, one herd

A merinod serves exactly one herd. Pointing one at two is not supported:
herdr numbers panes per workspace, so every herd has a `w1:p1` and the two
would collide — at best a confusing list, at worst a keystroke delivered to
the wrong machine.

Run one merinod per herd. Give each its own `listen` port **and** its own
`paths.stateDir`, or they will fight over the same credentials, device store
and access-gate files.

---

## When it does not work

**The agent list is empty but everything looks healthy.**
Almost always the socket's owner. Check the logs:

```
dial herdr socket ...: connect: permission denied
```

Run merinod as the user that owns `herdr.sock`. `/healthz` reports 200 through
all of this because it describes the merinod process, not the herd.

**`Load key: bad permissions`, then `Permission denied (publickey,password)`.**
The SSH key is group-readable. Kubernetes widens a Secret's file mode to match
the pod's `fsGroup`, which defeats `defaultMode: 0400`. The manifest copies
the key to a private path before use; if you have changed that, put it back.

**The pod never schedules and the PVC sits `Pending`.**
No StorageClass. Nothing in the events will say so. `kubectl get storageclass`,
then set `storageClassName`.

**The QR code points somewhere unreachable.**
`publicUrl` is unset, so merinod advertised a guess from its own interfaces.

**The tunnel comes up, then stops carrying traffic.**
A dropped connection the far end never noticed. The manifest sets
`ServerAliveInterval`, `ServerAliveCountMax` and `ExitOnForwardFailure` so ssh
gives up and autossh rebuilds it; if you have trimmed those, restore them.

## Do not bridge the socket over TCP

It is tempting to put `socat` in front of the socket and reach it over the
network. Do not.

herdr's socket is its entire control API — it can spawn agents and type into
live panes — and the socket file's permissions are the only thing guarding it.
Publishing that over plain TCP hands the whole API to anything that can reach
the port. SSH forwarding carries the same bytes but authenticates the far end
with a key first.

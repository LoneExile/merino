# Deploying `merinod`

`merinod` is Merino without a desktop — the same dashboard, run as a
background daemon. It must be able to **open** herdr's unix socket
(`~/.config/herdr/herdr.sock` by default): same host, a bind mount, or an
`ssh -L` forward. herdr never listens on a network port, so there is nothing
to dial remotely — only a socket *file* to reach.

## Shapes

| Shape | What | Verdict |
| --- | --- | --- |
| **A · systemd** (`build/systemd/merino.service`) | herdr and merinod on the same Linux host, same user | Simplest. Everything works: socket, session discovery, paste, spawn. |
| **B · Docker** (`compose.yaml`) | merinod container, `~/.config/herdr` bind-mounted read-only | Works. Mount the whole directory, never the bare socket file. |
| **C3 · SSH forward** (`k8s/merino-ssh.yaml`) | herd stays put; an `autossh` sidecar carries the socket into the pod | **Primary Kubernetes path.** No image to build for herdr itself, works through NAT, authenticated by a restricted SSH key. Paste and session-switching are broken under this shape — see the manifest's own comments. |
| **C1 · one pod** (`k8s/merino-pod.yaml`) | `herdr server` + merinod in one pod, sharing an `emptyDir` for the socket | Valid, but only once you've built and credentialed a `herdr` + agent-CLIs image — one does not exist yet. Documented alternative, not the default. |

Pick A whenever the option exists at all. Reach for B when herdr's host
should stay bare-metal but merinod should live in a container. C3 is the
Kubernetes default; C1 is what to build toward once a herdr image exists.

### Do not use a `socat` relay

A tempting fifth shape is a two-pod `socat` bridge: unix socket → TCP →
unix socket, no SSH involved. **Do not do this.** herdr's socket is its
entire control API — spawning agents, typing into live panes, everything
Merino itself can do — and the socket file's permissions are the only
access control in front of it today. Bridging it over plain TCP publishes
that whole API, unauthenticated, on the pod network: anything that can
reach the relay pod can act as an authenticated Merino client. `ssh -L`
(Shape C3) forwards the same bytes but authenticates the far end with a key
first; `socat` forwards them to anyone who asks.

## One `merinod` serves exactly one herd

This is a real limitation operators will hit, not a hypothetical one.
herdr's pane IDs are workspace-scoped and sequential — `w1:p1` on *every*
herdr server, independently. Point one `merinod` at two herds and their
IDs collide: one host's pane can silently overwrite the other's in
Merino's in-memory store, and a write routed by pane ID could type into
the wrong machine's terminal. Several hosts behind one unified dashboard is
a genuinely bigger feature (composite `herdID + paneID` identity through
the store, routes, audit and push), not something this deploy layer can
paper over.

**The answer today is one `merinod` per herd** — which works, at the cost
of a separate login and a separate device-pairing per herd. Each instance
needs its **own** `listen` port and its **own** `paths.stateDir` (or
`MERINO_CONFIG` pointing at a distinct `config.yml`): two instances sharing
either will fight over `bootstrap-creds.json`, the paired-device store, and
the three access-gate files, non-deterministically. There is no
coordination between instances to prevent this — it is entirely on the
operator to give each one a distinct port and a distinct state directory.

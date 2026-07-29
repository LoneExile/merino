# Contributing to Merino

Merino is a Go backend with a React frontend, shipped as a macOS menu bar app
(Wails v3) and a phone dashboard served from the same binary.

If you are here to *use* Merino, see [README.md](README.md).

## Setup

| Tool | Why |
| --- | --- |
| Go — version in `go.mod` | backend |
| Node 22+ | frontend build |
| [Wails v3](https://v3.wails.io) | `.app` packaging and Go↔TS bindings |
| [just](https://just.systems) | task runner |
| macOS | the menu bar app is the product; the web dashboard alone builds anywhere |

```bash
git clone https://github.com/LoneExile/merino.git
cd merino
just          # list every recipe
just app      # build the .app and launch it against your real herd
```

`just app` rebuilds every time on purpose. A stale bundle that silently
predates your change is the most confusing state this project has.

You need a running [herdr](https://herdr.dev) to develop against. Point at a
throwaway session instead of your real herd with:

```bash
just sock=~/.config/herdr/sessions/test/herdr.sock app
```

## The loop

```bash
just dev       # Vite hot reload — frontend only, Go changes need `just app`
just app       # package + launch the menu bar app
just debug     # foreground, MERINO_DEBUG=1, includes per-frame tray animation
just stop      # kill every running instance

export MERINO_PASS='…'
just web       # loopback dashboard
just web-lan   # LAN bind, cleartext HTTP — prefer a tunnel
just tunnel    # LAN bind + --behind-proxy, for a Docker cloudflared origin
```

The password comes from the environment, never argv.

`just app` is **not** the tunnel entry point: it runs the bundle with no flags,
so it binds `0.0.0.0:8730` but without `--behind-proxy`. Over a public tunnel
that means cookies are not marked secure and the login throttle counts the
proxy's IP. Use `just tunnel` for that.

### Flags and environment

These reach the **binary**, not the bundle. A `.app` launched from Finder,
Homebrew or login items is given no arguments and inherits no shell
environment, so none of this is reachable by someone running the installed
app — which is why the README does not list it.

| Flag / env | Purpose |
| --- | --- |
| `--listen ADDR` | Dashboard bind. Empty means `0.0.0.0:8730`, which is what the bundle uses. |
| `--behind-proxy` | Secure cookies, and trust the proxy's client-IP headers for the login throttle. Required behind a TLS terminator. |
| `--allow-writes` | Force phone writes on, overriding the persisted toggle. |
| `--allow-session-switch` | Let the dashboard repoint at a different herdr session. |
| `MERINO_USER` / `MERINO_PASS` | Operator credentials. Password via env, never argv. |
| `MERINO_PUBLIC_URL` | Public origin baked into pairing QR links. |
| `MERINO_DEBUG=1` | Verbose logging, including per-frame tray animation. |
| `HERDR_SOCK` | herdr socket path, if not the default. |

Anything a user should be able to change has to be a **persisted setting** the
panel can write, not a flag: see `session-switch.json`, `allow-writes.json` and
`password-login.json` under `~/Library/Logs/merino/` for the pattern.

## Before you push

```bash
just gate
```

That is exactly what CI runs: `gofmt`, `go vet`, `go test ./... -race`, the
TypeScript typecheck, the frontend checks, the Vite build, and a Go build. It
must exit 0.

## Architecture

```
herdr server (unix socket)
  ├── lifecycle connection  — global pane events
  └── status connection     — per-pane agent status
            │
         Store (Go, authoritative)
            ├── events ──▶ React panel / phone dashboard
            └── tray label + sheep animation
```

**Go owns state; React projects it.** The backend emits a full sorted agent
list on every change. The UI never merges deltas.

### Two transports, one interface

`frontend/src/client.ts` defines a `Client` the UI codes against. Two
implementations:

| | desktop | web |
| --- | --- | --- |
| module | `wailsClient.ts` | `client.ts` |
| transport | Wails IPC | HTTP + SSE |
| pane output | polling | push stream |
| authority | the operator, locally | `Policy` + the write gate + audit |

Capabilities are **optional methods**. `if (client.streamPane)` is a real
check, not a type dance — the desktop client genuinely has no stream, because
Wails marshals call arguments as JSON and a function has no JSON
representation.

`wailsClient.ts` must never be imported statically from anything the browser
can reach: importing `@wailsio/runtime` initialises
`window.webkit.messageHandlers` at module scope and throws in a plain browser.
It is loaded through a dynamic import from `makeClient()`.

### Writes are gated three times

Anything that reaches a live terminal passes:

1. **Build** — the route only exists when `Config.Writer != nil`
2. **Runtime** — `WritesAllowed()`, the toggle in Settings
3. **Identity** — `Policy.CanControl` for a pane, `Policy.CanSpawn` for a new one

Then `internal/app/guard.go` validates the payload itself, and the result is
audited whether it succeeded or was refused. A refusal with no record is the
one outcome worth reviewing that you would not be able to.

Adding a write route means adding it inside `mountWrites`, not beside it.

## Testing

Go uses the standard toolchain. The frontend has **no test runner** — Vite and
tsc only — so pure logic is exercised by standalone scripts in
`frontend/src/__checks__/`, run under Node's type stripping and wired into
`just gate`:

```bash
node --experimental-strip-types frontend/src/__checks__/uiOpen.check.ts
```

Note those files are outside the tsconfig include, so they are *not*
typechecked. A missing export fails at run time; a wrong argument type does
not.

### What a test has to be worth

A test that cannot fail is not a test. Before trusting a new one, **break the
code it covers and watch it go red**:

```bash
# neuter the behaviour, run the test, restore, run again
```

Two failure modes this repo has hit and now guards against:

- **Testing the helper, not the call site.** A unit test on a pure function
  stays green when the call site stops calling it. Cover the wiring too, or
  say plainly which layer is unverified.
- **Asserting "not the bad value".** `!= 200` passes for a `201` that means the
  route is live when it should not exist. Assert the determinate status.

Where a boundary belongs to another process, pin the *other side's* rule.
`internal/app/agentname_test.go` checks every derived agent name against
herdr's actual regex, because herdr rejects a bad one only after a tab has
already been created.

## The traps

Hard-won, each one shipped a bug at least once.

**herdr's read sources are not interchangeable.** `visible` is the screen;
`recent` is output since the last read and is empty for any settled pane.
Colour needs `format: "ansi"` — `strip_ansi: false` alone returns byte-identical
stripped text.

**Submitting to a TUI needs Enter as a key.** `pane.send_text` with `"\n"` sits
in the prompt. Line-based shells accept it, so this passes against zsh and
fails against every actual agent.

**A `.app` launched from Finder has almost no PATH.** It inherits
`/usr/bin:/bin:/usr/sbin:/sbin` — no mise, asdf, nvm, Homebrew or
`~/.local/bin`. Anything asking "is this installed?" must ask the user's login
shell, and must decide success from a sentinel the script prints, not from the
exit status (`sh -c` returns the *last* command's status) or from stdout being
non-empty (an rc-file banner satisfies that).

**A client timeout shorter than the operation abandons it.** `herdr.Client`
defaults to a 15 s `CallTimeout`, but `agent.start` waits up to 45 s for a cold
agent to reach its prompt. Hitting our deadline first would abandon a start
that is still running and roll back a tab whose agent then appears seconds
later. A long call gets its own client with a matching budget — `Client` holds
an atomic counter, so copy the socket, not the struct.

**Deep links are commands, not values.** Storing one as state means asking for
the same thing twice is a no-op. See `TabRequest.seq` in `uiOpen.ts`.

**The tray is unclickable from a headless session.** Anything it drives has to
be provable somewhere that is not the tray, which is why the `ui:open` grammar
lives in its own module with a check that reads `main.go` and fails when the
two drift.

**herdr confirms an agent before Merino's list does.** Opening a freshly
spawned pane eagerly trips the "this pane no longer exists" guard. Wait for it
to appear, and do not steal the view if the operator has navigated elsewhere.

## Design system

`design.md` is **locked**. Read it before touching UI. It fixes the palette,
type scale, motion, and rules like *exactly one scrolling region per screen*
and a 44 px minimum touch target. Extend or amend that file when the system
needs to grow; do not improvise per screen.

`PRODUCT.md` says who this is for and what it is not.

## Code style

- Comments explain **why**, not what. If a line needs explaining, it usually
  needs a reference to the failure that produced it.
- Match the surrounding conventions rather than importing your own.
- Delete code that stops being used. No shims, no `// removed` markers.
- Keep files decomposed. Around 1000 lines is the point to split *before*
  adding more, not after.

## Layout

```
main.go                     wiring: tray, panel window, flags, startup
internal/app/               store, guard, service — the authoritative state
internal/herdr/             the herdr socket client
internal/web/               HTTP dashboard, auth, policy, audit
internal/desktop/           macOS settings, autostart, updates
frontend/src/               React app: panel and phone dashboard
frontend/src/sheets/        one file per overlay sheet
frontend/src/__checks__/    standalone logic checks
build/darwin/               Info.plist, icons, packaging Taskfile
assets/sheep/               parametric drawing every sheep mark comes from
```

## Releasing

Maintainers only.

> **Never bump the version without the maintainer explicitly agreeing to it.**
> A version bump is a decision about what the release *means* — not
> housekeeping to tidy up alongside a change. Land the work first; the bump is
> a separate, deliberate act. If you think a release is due, say so and wait
> for a yes. This applies to agents and humans equally.

1. Agree that a release is happening, and on the number
2. Bump it in `frontend/package.json` (and its lockfile) and
   `build/darwin/Info.plist` / `Info.dev.plist`
3. `just changelog vX.Y.Z` — review the diff. git-cliff regenerates from
   commits, so re-add any hand-written upgrade note it drops
4. Add an upgrade note for anything that changes behaviour on an existing
   install. Commit subjects cannot say "this can lock you out"; a note can
5. `just gate`
6. Merge the release PR, then tag `vX.Y.Z` and push the tag —
   `.github/workflows/release.yml` builds, packages, and posts the CHANGELOG
   section as the release body
7. The Homebrew cask bump is automatic; check it landed

`just release-notes vX.Y.Z` prints exactly what the workflow will post.

## Pull requests

`main` is protected. Direct pushes are rejected for everyone including
admins, so every change arrives as a PR:

```bash
git checkout -b fix/some-thing
# work, commit
git push -u origin fix/some-thing
gh pr create --fill
```

CI (`build`) must be green and conversations resolved before merge. Approvals
are set to zero rather than one because GitHub forbids approving your own PR,
which would deadlock a solo maintainer — the gate is CI and the PR itself, not
a rubber stamp.

Merge with **squash** or **rebase**. Merge commits are disabled: their
`Merge pull request #12 from…` subject is not a Conventional Commit, and
git-cliff would either skip it or emit it as noise. Squash takes the PR title
as the subject, so write the title as a Conventional Commit.

- One logical change per PR. Split refactors from behaviour.
- `just gate` green.
- Say how you verified it. "Tests pass" is not a verification story; what you
  ran and what you observed is.
- Note anything you could **not** verify. An honest gap is worth more than a
  confident claim that does not hold.

### If protection has to come off

It should not, but a broken `build` can wedge an urgent fix. Lifting it is a
deliberate, visible act, not a quiet `--force`:

```bash
gh api -X DELETE repos/LoneExile/merino/branches/main/protection
# fix, push, then put it straight back — see git history for the payload
```

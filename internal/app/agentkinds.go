package app

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// AgentKind is one interactive agent Merino can start in a new pane.
type AgentKind struct {
	// Kind is herdr's identifier, passed verbatim to agent.start.
	Kind string `json:"kind"`
	// Label is what a human reads.
	Label string `json:"label"`
	// Path is where the executable was found, for the UI to show as evidence.
	Path string `json:"path"`
}

// supportedKinds mirrors the values herdr's `agent start --kind` accepts.
//
// herdr compiles this set in and exposes it nowhere over the socket: the API
// schema types `kind` as a bare string, and the only enumeration is the CLI's
// own --help. It is duplicated here rather than shelled out for, because the
// menu-bar app cannot rely on the herdr binary being on its PATH (see
// resolveKinds). herdr validates the kind anyway and answers
// unsupported_agent_kind, so the worst case for a stale entry is a clear
// error from the authority rather than a wrong spawn.
//
// The executable is the kind name for all current kinds; the pairs stay
// explicit so a future kind whose binary differs has somewhere to say so.
var supportedKinds = []struct {
	kind  string
	label string
	bin   string
}{
	{"omp", "Oh My Pi", "omp"},
	{"pi", "Pi", "pi"},
	{"claude", "Claude Code", "claude"},
	{"codex", "Codex", "codex"},
	{"gemini", "Gemini", "gemini"},
	{"cursor", "Cursor", "cursor"},
	{"opencode", "OpenCode", "opencode"},
	{"copilot", "Copilot", "copilot"},
	{"grok", "Grok", "grok"},
	{"amp", "Amp", "amp"},
	{"droid", "Droid", "droid"},
	{"kimi", "Kimi", "kimi"},
	{"kiro", "Kiro", "kiro"},
	{"kilo", "Kilo", "kilo"},
	{"cline", "Cline", "cline"},
	{"devin", "Devin", "devin"},
	{"agy", "Agy", "agy"},
	{"hermes", "Hermes", "hermes"},
	{"maki", "Maki", "maki"},
	{"mastracode", "Mastra Code", "mastracode"},
	{"qodercli", "Qoder", "qodercli"},
}

// kindCacheTTL bounds how stale the installed-agent list may be. Long enough
// that opening the sheet repeatedly costs one shell spawn, short enough that
// installing an agent shows up without restarting the app.
const kindCacheTTL = 2 * time.Minute

// lookupTimeout bounds the login-shell probe. A shell whose rc files hang
// must not hang the UI behind them.
const lookupTimeout = 6 * time.Second

type kindCache struct {
	mu   sync.Mutex
	at   time.Time
	list []AgentKind
}

var kinds kindCache

// AvailableAgentKinds returns the supported agents whose executable exists on
// this machine, newest lookup cached for kindCacheTTL.
func AvailableAgentKinds(ctx context.Context) []AgentKind {
	kinds.mu.Lock()
	defer kinds.mu.Unlock()

	if time.Since(kinds.at) < kindCacheTTL && kinds.list != nil {
		return kinds.list
	}
	kinds.list = resolveKinds(ctx)
	kinds.at = time.Now()
	return kinds.list
}

// resolveKinds finds which agent executables exist, asking a LOGIN shell
// rather than looking at this process's PATH.
//
// That distinction is the whole point. A .app launched from Finder or by
// launch-at-login inherits a minimal PATH (/usr/bin:/bin:/usr/sbin:/sbin),
// which contains none of mise, asdf, nvm, Homebrew or ~/.local/bin — so
// exec.LookPath would report almost nothing installed on a machine full of
// agents. herdr types the agent's command into a pane running the user's own
// interactive shell, so the user's login environment, not ours, is the one
// that decides whether a kind will actually start.
func resolveKinds(ctx context.Context) []AgentKind {
	bins := make([]string, 0, len(supportedKinds))
	for _, k := range supportedKinds {
		bins = append(bins, k.bin)
	}

	found := lookupInLoginShell(ctx, bins)
	if found == nil {
		// Shell unavailable or misbehaving: fall back to our own PATH. It
		// under-reports under Finder launch, but a short list beats none.
		found = map[string]string{}
		for _, b := range bins {
			if p, err := exec.LookPath(b); err == nil {
				found[b] = p
			}
		}
	}

	out := make([]AgentKind, 0, len(found))
	for _, k := range supportedKinds {
		if p, ok := found[k.bin]; ok {
			out = append(out, AgentKind{Kind: k.kind, Label: k.label, Path: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// lookupInLoginShell runs one `command -v` per binary inside a single
// interactive login shell and parses the resolved paths back out.
//
// One shell for the whole set, not one per binary: rc files can take hundreds
// of milliseconds, and twenty-one of those in series is a visibly slow sheet.
// Returns nil (not an empty map) when the shell itself could not be run, so
// the caller can tell "no agents installed" from "could not ask".
func lookupInLoginShell(ctx context.Context, bins []string) map[string]string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return nil
	}

	var b strings.Builder
	for _, bin := range bins {
		// Print "<name>\t<path>" per hit; misses print nothing. command -v is
		// POSIX and works in sh, bash, zsh and fish-as-login-sh alike.
		b.WriteString("p=$(command -v ")
		b.WriteString(bin)
		b.WriteString(" 2>/dev/null) && printf '")
		b.WriteString(bin)
		b.WriteString("\\t%s\\n' \"$p\"\n")
	}

	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	// -l so rc files that set up version managers run; -i because zsh only
	// sources ~/.zshrc (where mise/asdf usually land) for interactive shells.
	cmd := exec.CommandContext(ctx, shell, "-lic", b.String())
	cmd.Env = append(os.Environ(), "TERM=dumb")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}

	found := make(map[string]string, len(bins))
	for _, line := range strings.Split(string(out), "\n") {
		name, path, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || name == "" || path == "" {
			continue
		}
		found[name] = path
	}
	return found
}

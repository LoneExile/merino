package app

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Live harness catalogs supplement the static builtinSlash table.
// omp/pi: parse installed @oh-my-pi/pi-coding-agent dist/cli.js
// claude: parse `claude commands` stdout
// Cached so typeahead does not spawn work on every keystroke.

var (
	harnessMu    sync.Mutex
	harnessCache = map[string]harnessSnap{}
)

type harnessSnap struct {
	at   time.Time
	cmds []SlashCommand
}

const harnessTTL = 2 * time.Minute

var reOmpSlash = regexp.MustCompile(`\{name:"([a-z][a-z0-9:_-]{1,40})",description:"([^"]*)"`)

// normalizeAgentKind maps herdr labels onto catalog keys.
// "Claude Code", "claude", "omp " → claude / omp.
func normalizeAgentKind(agent string) string {
	s := strings.ToLower(strings.TrimSpace(agent))
	if s == "" {
		return ""
	}
	// First token only ("claude code" → "claude").
	if i := strings.IndexAny(s, " \t/"); i > 0 {
		s = s[:i]
	}
	switch {
	case strings.HasPrefix(s, "claude"):
		return "claude"
	case s == "pi" || strings.HasPrefix(s, "pi-"):
		return "pi"
	case s == "omp" || strings.Contains(s, "oh-my-pi") || s == "ohmy pi":
		return "omp"
	case strings.HasPrefix(s, "grok"):
		return "grok"
	default:
		return s
	}
}

func harnessCommands(kind string) []SlashCommand {
	kind = normalizeAgentKind(kind)
	switch kind {
	case "omp", "pi":
		// Shared OMP slash surface.
		return cachedHarness("omp", loadOmpSlashCommands)
	case "claude":
		return cachedHarness("claude", loadClaudeSlashCommands)
	case "grok":
		return append([]SlashCommand(nil), builtinSlash["grok"]...)
	default:
		return nil
	}
}

func cachedHarness(key string, load func() []SlashCommand) []SlashCommand {
	harnessMu.Lock()
	defer harnessMu.Unlock()
	if snap, ok := harnessCache[key]; ok && time.Since(snap.at) < harnessTTL {
		return snap.cmds
	}
	cmds := load()
	harnessCache[key] = harnessSnap{at: time.Now(), cmds: cmds}
	return cmds
}

func loadOmpSlashCommands() []SlashCommand {
	path := ompCLIJS()
	if path == "" {
		return append([]SlashCommand(nil), builtinSlash["omp"]...)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return append([]SlashCommand(nil), builtinSlash["omp"]...)
	}
	text := string(data)
	// Anchor near a known slash entry so we do not harvest unrelated objects.
	start := strings.Index(text, `name:"btw",description:`)
	if start < 0 {
		start = strings.Index(text, `name:"help",description:"Show help`)
	}
	if start < 0 {
		start = 0
	}
	lo := start - 80_000
	if lo < 0 {
		lo = 0
	}
	hi := start + 400_000
	if hi > len(text) {
		hi = len(text)
	}
	window := text[lo:hi]

	// Prefer entries that also mention handleTui / allowArgs — real slash cmds.
	reStrong := regexp.MustCompile(`\{name:"([a-z][a-z0-9:_-]{1,40})",description:"([^"]*)"[^}]{0,300}(?:handleTui|allowArgs)`)

	seen := map[string]struct{}{}
	var out []SlashCommand
	add := func(name, desc string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, SlashCommand{
			Name:        name,
			Value:       "/" + name,
			Description: truncateDesc(desc, 140),
			Source:      "builtin",
		})
	}
	for _, m := range reStrong.FindAllStringSubmatch(window, -1) {
		add(m[1], m[2])
	}
	// Also take plain name/description pairs in a tighter slash-y window if thin.
	if len(out) < 15 {
		for _, m := range reOmpSlash.FindAllStringSubmatch(window, -1) {
			name := m[1]
			if name == "winston" || name == "task" || name == "sonic" {
				continue
			}
			add(name, m[2])
		}
	}
	// Guarantee static fallbacks (help, status, …) even if parse fails partially.
	for _, c := range builtinSlash["omp"] {
		add(c.Name, c.Description)
	}
	// btw is critical and easy to miss in static lists.
	add("btw", "Ask an ephemeral side question using the current session context")
	return out
}

func ompCLIJS() string {
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".bun/install/global/node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js"),
		)
	}
	for _, bin := range []string{"omp", "pi"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		dir := filepath.Dir(path)
		candidates = append(candidates,
			filepath.Join(dir, "../install/global/node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js"),
			filepath.Join(dir, "node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js"),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func loadClaudeSlashCommands() []SlashCommand {
	seen := map[string]struct{}{}
	var out []SlashCommand
	add := func(name, desc string) {
		name = strings.TrimSpace(name)
		name = strings.TrimPrefix(name, "/")
		if i := strings.IndexAny(name, " \t["); i > 0 {
			name = name[:i]
		}
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 48 {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, SlashCommand{
			Name:        name,
			Value:       "/" + name,
			Description: truncateDesc(desc, 140),
			Source:      "builtin",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "commands")
	cmd.Env = os.Environ()
	if raw, err := cmd.Output(); err == nil {
		// Backtick-wrapped commands: `/btw` or `/loop [interval]`
		reTick := regexp.MustCompile("`" + `(/[a-zA-Z][\w:-]*)` + "`")
		for _, m := range reTick.FindAllStringSubmatch(string(raw), -1) {
			add(m[1], "")
		}
		// Lines that are just command lists without ticks.
		sc := bufio.NewScanner(bytes.NewReader(raw))
		reBare := regexp.MustCompile(`(/[a-zA-Z][\w:-]{1,40})`)
		for sc.Scan() {
			line := sc.Text()
			for _, m := range reBare.FindAllStringSubmatch(line, -1) {
				add(m[1], "")
			}
		}
	}

	for _, c := range builtinSlash["claude"] {
		add(c.Name, c.Description)
	}
	add("btw", "Side question without derailing the main turn")
	return out
}

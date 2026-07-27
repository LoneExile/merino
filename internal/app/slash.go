package app

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SlashCommand is one entry in the composer typeahead.
//
// Name is what the user types after '/' (no leading slash). Value is the full
// text inserted into the composer (usually "/"+Name, sometimes "skill:Name "
// for omp skill dispatch).
type SlashCommand struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"` // builtin | skill | plugin
}

// MaxSlashResults caps the typeahead list so a phone-sized sheet stays usable.
const MaxSlashResults = 40

// Builtin slash commands known for each harness. Not exhaustive — each agent
// also loads skills/plugins at runtime — but enough that typing "/" feels
// alive without an IPC round-trip into the TUI.
//
// Keys are lowercased agent labels as herdr reports them (omp, pi, claude, grok).
var builtinSlash = map[string][]SlashCommand{
	"omp": {
		{Name: "help", Value: "/help", Description: "Show help", Source: "builtin"},
		{Name: "btw", Value: "/btw ", Description: "Side question using session context", Source: "builtin"},
		{Name: "status", Value: "/status", Description: "Session / connection status", Source: "builtin"},
		{Name: "model", Value: "/model ", Description: "Switch model for this session", Source: "builtin"},
		{Name: "plan", Value: "/plan", Description: "Toggle plan mode", Source: "builtin"},
		{Name: "clear", Value: "/clear", Description: "Clear conversation", Source: "builtin"},
		{Name: "compact", Value: "/compact", Description: "Compact context", Source: "builtin"},
		{Name: "context", Value: "/context", Description: "Context usage breakdown", Source: "builtin"},
		{Name: "tools", Value: "/tools", Description: "Tools visible to the agent", Source: "builtin"},
		{Name: "todo", Value: "/todo", Description: "View or modify todos", Source: "builtin"},
		{Name: "session", Value: "/session ", Description: "Session management", Source: "builtin"},
		{Name: "usage", Value: "/usage", Description: "Provider usage and limits", Source: "builtin"},
		{Name: "mcp", Value: "/mcp ", Description: "Manage MCP servers", Source: "builtin"},
		{Name: "plugins", Value: "/plugins", Description: "Manage plugins", Source: "builtin"},
		{Name: "marketplace", Value: "/marketplace", Description: "Browse / install plugins", Source: "builtin"},
		{Name: "login", Value: "/login", Description: "OAuth login", Source: "builtin"},
		{Name: "logout", Value: "/logout", Description: "OAuth logout", Source: "builtin"},
		{Name: "hotkeys", Value: "/hotkeys", Description: "Keyboard shortcuts", Source: "builtin"},
		{Name: "export", Value: "/export", Description: "Export session to HTML", Source: "builtin"},
		{Name: "share", Value: "/share", Description: "Share encrypted session link", Source: "builtin"},
		{Name: "tree", Value: "/tree", Description: "Navigate session tree", Source: "builtin"},
		{Name: "branch", Value: "/branch", Description: "Branch from a previous message", Source: "builtin"},
		{Name: "fork", Value: "/fork", Description: "Fork from a previous message", Source: "builtin"},
		{Name: "copy", Value: "/copy", Description: "Copy text from the conversation", Source: "builtin"},
		{Name: "queue", Value: "/queue ", Description: "Queue a message after yield", Source: "builtin"},
		{Name: "settings", Value: "/settings", Description: "Open settings", Source: "builtin"},
		{Name: "reload-plugins", Value: "/reload-plugins", Description: "Reload plugins and skills", Source: "builtin"},
	},
	"pi": {
		// Pi shares OMP's slash surface for the common set.
		{Name: "help", Value: "/help", Description: "Show help", Source: "builtin"},
		{Name: "model", Value: "/model ", Description: "Switch model", Source: "builtin"},
		{Name: "clear", Value: "/clear", Description: "Clear conversation", Source: "builtin"},
		{Name: "compact", Value: "/compact", Description: "Compact context", Source: "builtin"},
		{Name: "context", Value: "/context", Description: "Context usage", Source: "builtin"},
		{Name: "tools", Value: "/tools", Description: "Visible tools", Source: "builtin"},
		{Name: "todo", Value: "/todo", Description: "Todos", Source: "builtin"},
		{Name: "session", Value: "/session ", Description: "Session management", Source: "builtin"},
		{Name: "mcp", Value: "/mcp ", Description: "MCP servers", Source: "builtin"},
		{Name: "settings", Value: "/settings", Description: "Settings", Source: "builtin"},
	},
	"claude": {
		{Name: "help", Value: "/help", Description: "Help overview", Source: "builtin"},
		{Name: "clear", Value: "/clear", Description: "Clear conversation", Source: "builtin"},
		{Name: "compact", Value: "/compact", Description: "Compact context", Source: "builtin"},
		{Name: "config", Value: "/config", Description: "Open config", Source: "builtin"},
		{Name: "model", Value: "/model", Description: "Switch model", Source: "builtin"},
		{Name: "fast", Value: "/fast", Description: "Toggle fast mode", Source: "builtin"},
		{Name: "mcp", Value: "/mcp", Description: "MCP servers", Source: "builtin"},
		{Name: "init", Value: "/init", Description: "Initialize project context", Source: "builtin"},
		{Name: "review", Value: "/review", Description: "Code review", Source: "builtin"},
		{Name: "code-review", Value: "/code-review", Description: "Code review", Source: "builtin"},
		{Name: "security-review", Value: "/security-review", Description: "Security review", Source: "builtin"},
		{Name: "simplify", Value: "/simplify", Description: "Simplify code", Source: "builtin"},
		{Name: "loop", Value: "/loop", Description: "Loop / iterate", Source: "builtin"},
		{Name: "schedule", Value: "/schedule", Description: "Schedule work", Source: "builtin"},
		{Name: "run", Value: "/run", Description: "Run a command flow", Source: "builtin"},
		{Name: "skill", Value: "/skill ", Description: "Invoke a skill", Source: "builtin"},
		{Name: "btw", Value: "/btw ", Description: "Side question", Source: "builtin"},
		{Name: "keybindings", Value: "/keybindings", Description: "Customize keybindings", Source: "builtin"},
		{Name: "feedback", Value: "/feedback", Description: "Report bugs / request features", Source: "builtin"},
		{Name: "powerup", Value: "/powerup", Description: "Features most people miss", Source: "builtin"},
	},
	"grok": {
		{Name: "help", Value: "/help", Description: "Show help", Source: "builtin"},
		{Name: "tutorial", Value: "/tutorial", Description: "Interactive tutorial", Source: "builtin"},
		{Name: "clear", Value: "/clear", Description: "Clear conversation", Source: "builtin"},
		{Name: "model", Value: "/model", Description: "Switch model", Source: "builtin"},
		{Name: "compact", Value: "/compact", Description: "Compact context", Source: "builtin"},
	},
}

// slashSkillCache avoids re-walking the skill tree on every keystroke.
var (
	slashSkillMu    sync.Mutex
	slashSkillCache []SlashCommand
	slashSkillAt    time.Time
)

const slashSkillTTL = 30 * time.Second

// FilterSlashCommands returns typeahead hits for query (with or without a
// leading '/'). agent is the herdr agent label (omp/pi/claude/grok/…).
// cwd, when non-empty, also loads project-local skills/commands from that tree.
// safeSkillCwd rejects empty, relative, and ".." paths so a compromised
// store value cannot walk arbitrary trees. Returns cleaned abs path or "".
func safeSkillCwd(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return ""
	}
	clean := filepath.Clean(cwd)
	if clean == "." || strings.Contains(clean, "..") {
		return ""
	}
	// filepath.Clean already collapses ..; require still absolute.
	if !filepath.IsAbs(clean) {
		return ""
	}
	return clean
}

func FilterSlashCommands(agent, query, cwd string) []SlashCommand {
	q := strings.TrimSpace(query)
	q = strings.TrimPrefix(q, "/")
	q = strings.ToLower(q)

	kind := normalizeAgentKind(agent)

	// 1) Live harness builtins (omp cli.js / claude commands) — first so they
	// win dedup over skills with the same name.
	// 2) Static fallback table.
	// 3) Skills + project-local commands.
	pool := append([]SlashCommand(nil), harnessCommands(kind)...)
	pool = append(pool, builtinSlash[kind]...)
	if kind == "pi" {
		pool = append(pool, builtinSlash["omp"]...)
		pool = append(pool, harnessCommands("omp")...)
	}

	skills := loadSlashSkills(safeSkillCwd(cwd))
	for _, sk := range skills {
		switch kind {
		case "omp", "pi":
			pool = append(pool, SlashCommand{
				Name:        "skill:" + sk.Name,
				Value:       "skill:" + sk.Name + " ",
				Description: sk.Description,
				Source:      "skill",
			})
			pool = append(pool, SlashCommand{
				Name:        sk.Name,
				Value:       "/" + sk.Name,
				Description: sk.Description,
				Source:      "skill",
			})
		default:
			pool = append(pool, SlashCommand{
				Name:        sk.Name,
				Value:       "/" + sk.Name,
				Description: sk.Description,
				Source:      "skill",
			})
		}
	}

	// Dedup by name (first wins → harness builtins beat skills).
	seen := map[string]struct{}{}
	out := make([]SlashCommand, 0, len(pool))
	for _, c := range pool {
		key := strings.ToLower(c.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		if q != "" && !strings.HasPrefix(key, q) && !strings.Contains(key, q) {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}

	sort.SliceStable(out, func(i, j int) bool {
		ni, nj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		// Builtins before skills when both match.
		bi, bj := out[i].Source == "builtin" || out[i].Source == "", out[j].Source == "builtin" || out[j].Source == ""
		pi, pj := q != "" && strings.HasPrefix(ni, q), q != "" && strings.HasPrefix(nj, q)
		if pi != pj {
			return pi
		}
		if bi != bj {
			return bi
		}
		if len(ni) != len(nj) {
			return len(ni) < len(nj)
		}
		return ni < nj
	})

	if len(out) > MaxSlashResults {
		out = out[:MaxSlashResults]
	}
	return out
}

func loadSlashSkills(cwd string) []SlashCommand {
	// Home skills are cached; project-local commands always re-scan (cheap + cwd-specific).
	homeSkills := loadHomeSkills()
	if cwd == "" {
		return homeSkills
	}
	seen := map[string]struct{}{}
	out := make([]SlashCommand, 0, len(homeSkills)+16)
	for _, c := range homeSkills {
		seen[strings.ToLower(c.Name)] = struct{}{}
		out = append(out, c)
	}
	// Project skill dirs
	for _, root := range []string{
		filepath.Join(cwd, ".agents", "skills"),
		filepath.Join(cwd, ".claude", "skills"),
		filepath.Join(cwd, ".omp", "skills"),
	} {
		for _, c := range readSkillDirs(root) {
			key := strings.ToLower(c.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, c)
		}
	}
	// Claude-style project commands: .claude/commands/*.md → /name
	cmdDir := filepath.Join(cwd, ".claude", "commands")
	entries, err := os.ReadDir(cmdDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			base := e.Name()
			if !strings.HasSuffix(strings.ToLower(base), ".md") {
				continue
			}
			name := strings.TrimSuffix(base, filepath.Ext(base))
			key := strings.ToLower(name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			desc := skillDescription(filepath.Join(cmdDir, base))
			out = append(out, SlashCommand{
				Name:        name,
				Value:       "/" + name,
				Description: desc,
				Source:      "plugin",
			})
		}
	}
	return out
}

func loadHomeSkills() []SlashCommand {
	slashSkillMu.Lock()
	defer slashSkillMu.Unlock()
	if time.Since(slashSkillAt) < slashSkillTTL && slashSkillCache != nil {
		return slashSkillCache
	}
	home, _ := os.UserHomeDir()
	var out []SlashCommand
	seen := map[string]struct{}{}
	for _, root := range []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".omp", "agent", "skills"),
	} {
		for _, c := range readSkillDirs(root) {
			key := strings.ToLower(c.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, c)
		}
	}
	slashSkillCache = out
	slashSkillAt = time.Now()
	return out
}

func readSkillDirs(root string) []SlashCommand {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []SlashCommand
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		desc := skillDescription(filepath.Join(root, name, "SKILL.md"))
		out = append(out, SlashCommand{
			Name:        name,
			Value:       "/" + name,
			Description: desc,
			Source:      "skill",
		})
	}
	return out
}

// skillDescription pulls a one-line summary from SKILL.md frontmatter or the
// first non-empty body line. Empty on any read failure.
func skillDescription(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	inFront := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "---" {
			if !inFront {
				inFront = true
				continue
			}
			// end frontmatter
			inFront = false
			continue
		}
		if inFront {
			if strings.HasPrefix(strings.ToLower(line), "description:") {
				d := strings.TrimSpace(line[len("description:"):])
				d = strings.Trim(d, `"'`)
				if d != "" {
					return truncateDesc(d, 140)
				}
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return truncateDesc(line, 140)
	}
	return ""
}

func truncateDesc(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

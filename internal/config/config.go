// Package config loads the optional merino config.yml.
//
// Zero config stays genuinely zero. Merino starts with no flags and no file,
// generates its own credentials and binds 0.0.0.0:8730; every key here is
// optional and absent means exactly today's behaviour. No file anywhere is a
// supported, normal state — not a degraded one.
//
// # Precedence
//
//	flag  >  env  >  config.yml  >  built-in default
//
// Env sits above the file deliberately: MERINO_PASS, HERDR_SOCK and
// MERINO_DEBUG are documented today and used by `just web` / `just tunnel`.
// Demoting them below a file would break the dev loop silently.
//
// # Layers are not merged
//
// The search order stops at the first file it finds. "Which file set this?"
// must have exactly one answer, because Path reports it and a future
// `merinod config path` prints it.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileName is the config file's name in every directory searched.
const FileName = "config.yml"

// Config mirrors config.yml exactly. Pointer fields are the keys where
// "absent" and "false" must stay distinguishable: a gate the operator never
// mentioned is not the same as one they turned off, and only the second is
// allowed to override a setting made in the panel.
type Config struct {
	Listen      string `yaml:"listen"`
	PublicURL   string `yaml:"publicUrl"`
	BehindProxy *bool  `yaml:"behindProxy"`

	Herdr  Herdr  `yaml:"herdr"`
	Access Access `yaml:"access"`
	Auth   Auth   `yaml:"auth"`
	Paths  Paths  `yaml:"paths"`
	Log    Log    `yaml:"log"`
}

// Herdr locates the herd and describes what can be spawned on it.
type Herdr struct {
	// Socket is empty for ~/.config/herdr/herdr.sock. Always a FILE both
	// processes can open: the same host, a bind mount, or an ssh -L forward.
	Socket string `yaml:"socket"`

	// Agents overrides autodetection of which agent kinds the spawn sheet
	// may offer. Autodetection probes Merino's OWN login shell, which is
	// right on a workstation and wrong the moment herdr lives elsewhere.
	Agents []string `yaml:"agents"`
}

// Access holds the only three keys that collide with the panel's own
// toggles. See Gate for how that collision is resolved.
type Access struct {
	AllowWrites        *bool `yaml:"allowWrites"`
	AllowSessionSwitch *bool `yaml:"allowSessionSwitch"`
	PasswordLogin      *bool `yaml:"passwordLogin"`
}

// Auth configures the operator identity. There is deliberately no password
// key and no --password flag: a literal password never belongs in a file
// that gets copied around, and argv is world-readable.
type Auth struct {
	User         string `yaml:"user"`
	PasswordFile string `yaml:"passwordFile"`
}

// Paths relocates state. Moving stateDir on an existing install orphans every
// paired device and every push subscription, silently — the phone simply
// stops being signed in.
type Paths struct {
	StateDir string `yaml:"stateDir"`

	// AuditLog set to "-" writes JSONL to stdout for a log collector.
	AuditLog string `yaml:"auditLog"`
}

// Log configures the handler.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

// File is a loaded config plus where it came from and whether that source can
// be written. Both halves are needed: writability decides whether the three
// access keys are defaults or pins.
type File struct {
	Config

	// Path is the file that was loaded, or "" when none was found. "" is
	// normal, not an error.
	Path string

	// Writable reports whether Path can be opened for writing. Meaningless
	// when Path is "".
	//
	// This is the D1 policy: the filesystem decides. A config you can edit
	// is advice the panel may override; one you cannot is operator intent
	// that pins. Container and GitOps deploys therefore lock themselves
	// with no mode flag and no extra key.
	Writable bool

	// Unhonoured names keys that were parsed but that this build does not
	// act on yet. Reported rather than ignored: a key that silently does
	// nothing is worse than one that is rejected.
	Unhonoured []string
}

// Locked reports whether config.yml pins the access gates — a file exists and
// cannot be written, so the panel has no way to disagree with it.
func (f *File) Locked() bool { return f != nil && f.Path != "" && !f.Writable }

// GateSource names which rung of the precedence ladder decided a gate. It is
// reported so the UI can say "set by config.yml (read-only)" rather than
// leaving the operator to infer why a toggle will not move.
type GateSource string

const (
	GateFlag     GateSource = "flag"
	GateConfig   GateSource = "config.yml"
	GateSettings GateSource = "settings"
	GateDefault  GateSource = "default"
)

// Gate is one resolved access gate.
type Gate struct {
	On     bool
	Locked bool
	Source GateSource
}

// GateInputs is everything that has an opinion about one gate.
type GateInputs struct {
	// FlagOn is a CLI flag explicitly turning the gate on. These flags are
	// force-on only — false means "not given", which is why there is no
	// separate FlagSet.
	FlagOn bool

	// Config is the config.yml value, nil when the key is absent.
	Config *bool

	// SettingsExplicit and SettingsOn are the panel's JSON side-file.
	// Explicit is false when the operator has never touched the toggle.
	SettingsExplicit bool
	SettingsOn       bool

	// Default is the built-in behaviour when nothing else has an opinion.
	Default bool
}

// ResolveGate applies D1.
//
// A writable config.yml is a *default*: the panel's side-file outranks it and
// toggling works exactly as it does today. A read-only config.yml *pins*: the
// key wins over the side-file and Locked says so.
//
// A CLI flag still beats both, per the documented precedence. That is not a
// hole in D1 — D1 is about the panel, which cannot write a read-only file. A
// flag is a deliberate act by whoever started the process, and the caller
// logs when one overrides a locked key.
func (f *File) ResolveGate(in GateInputs) Gate {
	locked := f.Locked()
	switch {
	case in.FlagOn:
		return Gate{On: true, Locked: false, Source: GateFlag}
	case in.Config != nil && locked:
		return Gate{On: *in.Config, Locked: true, Source: GateConfig}
	case in.SettingsExplicit:
		return Gate{On: in.SettingsOn, Locked: false, Source: GateSettings}
	case in.Config != nil:
		return Gate{On: *in.Config, Locked: false, Source: GateConfig}
	default:
		return Gate{On: in.Default, Locked: false, Source: GateDefault}
	}
}

// String resolves one non-gate string key: flag > env > file > default.
// Empty means "not set" for all three inputs, which is true of every string
// key in the schema.
func String(flagVal, envVal, fileVal, def string) string {
	for _, v := range []string{flagVal, envVal, fileVal} {
		if v != "" {
			return v
		}
	}
	return def
}

// Search returns the paths Load will try, in order. Exported so a diagnostic
// command can show the operator exactly where Merino looked.
func Search(explicit string) []string {
	if explicit != "" {
		return []string{explicit}
	}
	var paths []string
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		paths = append(paths, filepath.Join(dir, "merino", FileName))
	} else if home, err := os.UserHomeDir(); err == nil {
		// Deliberately not os.UserConfigDir(), which is
		// ~/Library/Application Support on macOS. Merino exists because of
		// herdr, herdr keeps its socket and sessions in ~/.config/herdr,
		// and internal/app/sessions.go already walks that path. Sitting
		// next to it beats platform purity.
		paths = append(paths, filepath.Join(home, ".config", "merino", FileName))
	}
	return append(paths, filepath.Join("/etc", "merino", FileName))
}

// Load reads the first config file that exists, in Search order.
//
// explicit comes from --config or $MERINO_CONFIG. When set, a missing file is
// a fatal error rather than a fallback: an operator who named a file and got
// defaults instead has been lied to. When unset, finding nothing returns an
// empty File and no error.
func Load(explicit string) (*File, error) {
	if explicit == "" {
		explicit = os.Getenv("MERINO_CONFIG")
	}
	for _, path := range Search(explicit) {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		f := &File{Path: path, Writable: writable(path)}
		if err := decode(raw, &f.Config); err != nil {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
		f.Unhonoured = f.unhonoured()
		return f, nil
	}
	if explicit != "" {
		return nil, fmt.Errorf("config: %s: %w", explicit, os.ErrNotExist)
	}
	return &File{}, nil
}

// decode rejects unknown fields. A file where `publicURL` silently does
// nothing because the key is `publicUrl` is the exact quiet failure this
// whole package exists to avoid; better to refuse to start and name the typo.
func decode(raw []byte, into *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	// An empty or comment-only file decodes to io.EOF. That is a valid
	// config: every key is optional, so a file that sets none of them is a
	// file that changes nothing.
	if err := dec.Decode(into); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// writable probes the FILE, not its directory.
//
// The directory is the tempting probe and it is wrong. The common container
// shape is a read-only file inside a writable directory —
// `-v ./config.yml:/etc/merino/config.yml:ro`, or a ConfigMap subPath — and a
// directory probe calls that writable. The gates would then quietly degrade
// to defaults and the panel's side-file would override operator intent.
//
// O_WRONLY without O_TRUNC or O_APPEND opens without modifying a byte.
//
// Caveat: root ignores permission bits, so a 0444 file owned by someone else
// probes as writable when running as root. A read-only *mount* still returns
// EROFS even for root, which is the case that actually matters here.
func writable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// unhonoured lists keys this build parses but does not act on yet. They are
// in the schema because the schema is the published contract; they are
// reported because an operator setting a key and getting nothing deserves to
// be told, not left to discover it.
func (f *File) unhonoured() []string {
	var out []string
	if f.Auth.User != "" {
		out = append(out, "auth.user")
	}
	if f.Auth.PasswordFile != "" {
		out = append(out, "auth.passwordFile")
	}
	if f.Paths.StateDir != "" {
		out = append(out, "paths.stateDir")
	}
	if f.Paths.AuditLog != "" {
		out = append(out, "paths.auditLog")
	}
	if f.Log.Format != "" {
		out = append(out, "log.format")
	}
	return out
}

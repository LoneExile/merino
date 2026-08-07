package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/LoneExile/merino/internal/config"
)

// configTemplate seeds a fresh config.yml. Every key is commented out so the
// file, once created, still means "zero config" until the operator edits it —
// opening the button must never silently change how Merino runs. It documents
// the OAuth block and, loudly, the one rule that keeps this file safe to
// commit: the client secret is never written here.
const configTemplate = `# merino config.yml
#
# Everything here is optional. An empty file means today's behaviour.
# Precedence, highest first:  flag > env (MERINO_*) > this file > default.
# Uncomment a key to set it. Strict parser: an unknown key stops startup.

# listen: "0.0.0.0:8730"      # dashboard bind
# publicUrl: "https://merino.example"   # base for QR + OAuth redirect URLs
# behindProxy: false          # true marks cookies Secure, trusts proxy client-IP

# access:                     # these three collide with the panel toggles;
#   allowWrites: false        # a read-only config.yml pins them (panel shows
#   allowSessionSwitch: false # "set by config.yml"), a writable one is a default.
#   passwordLogin: false

# auth:
#   user: "operator"
#   passwordFile: "/run/secrets/merino-pass"  # never a password key inline

# Browser/phone single sign-on. Non-secret fields only — the client SECRET is
# read from clientSecretFile (or MERINO_GITHUB_CLIENT_SECRET /
# MERINO_OIDC_CLIENT_SECRET), NEVER written in this file, which gets committed.
# A provider set here is read-only in the Settings UI. Restart to apply edits.
# oauth:
#   github:
#     clientID: "Ov23liXXXXXXXXXXXXXX"   # the OAuth app's Client ID, NOT the secret
#     clientSecretFile: "/run/secrets/merino-github"
#     allow: ["your-gh-login"]   # usernames; OR use org/team
#     org: ""
#     team: ""
#     label: "Sign in with GitHub"
#   oidc:                        # Keycloak / any OIDC issuer
#     clientID: "merino"
#     clientSecretFile: "/run/secrets/merino-oidc"
#     issuer: "https://keycloak.example/realms/main"
#     allowRole: "herd-admin"    # required realm role
#     label: "Sign in with Keycloak"
`

// ConfigInfo is the Settings view of config.yml: where it is and whether it
// exists yet. The frontend uses this to label the "Open config file" button.
type ConfigInfo struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// configTarget is the file OpenConfigFile acts on: the loaded config if one was
// found, else the default user path (~/.config/merino/config.yml).
func (s *Settings) configTarget() (string, error) {
	if s.ConfigPath != "" {
		return s.ConfigPath, nil
	}
	cands := config.Search("")
	if len(cands) == 0 {
		return "", fmt.Errorf("no config location available")
	}
	return cands[0], nil // user path first; /etc is the last resort
}

// ConfigFileInfo reports the effective config path and whether it exists.
func (s *Settings) ConfigFileInfo() (ConfigInfo, error) {
	path, err := s.configTarget()
	if err != nil {
		return ConfigInfo{}, err
	}
	_, statErr := os.Stat(path)
	return ConfigInfo{Path: path, Exists: statErr == nil}, nil
}

// OpenConfigFile opens config.yml in the OS default editor, creating it from a
// fully-commented starter template first if it does not exist. Returns the
// path opened. Creating from the (all-commented) template is behaviour-neutral:
// the file still means "zero config" until the operator edits it.
func (s *Settings) OpenConfigFile() (string, error) {
	path, err := s.configTarget()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(configTemplate), 0o644); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := openInEditor(path); err != nil {
		return "", err
	}
	return path, nil
}

// openInEditor hands the file to the OS default handler for its type. macOS is
// the shipped desktop target; the others keep the method honest on dev boxes.
func openInEditor(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

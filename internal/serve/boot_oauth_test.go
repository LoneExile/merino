package serve

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/LoneExile/merino/internal/config"
)

func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// A config.yml block with no client ID owns nothing: set=false, so the store
// falls through to the env/UI layers rather than pinning an empty provider.
func TestResolveConfigGitHubUnsetWithoutClientID(t *testing.T) {
	cfg, set := resolveConfigGitHub(config.OAuthGitHub{Allow: []string{"lex"}}, quietLogger())
	if set {
		t.Fatalf("a github block with no clientID must be unset, got %+v", cfg)
	}
}

// The client secret comes from clientSecretFile (never inline), with the
// trailing newline a Secret mount leaves trimmed off.
func TestResolveConfigGitHubReadsSecretFile(t *testing.T) {
	t.Setenv("MERINO_GITHUB_CLIENT_SECRET", "")
	t.Setenv("HERDR_TUNNEL_GITHUB_CLIENT_SECRET", "")
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "gh-secret")
	if err := os.WriteFile(secretPath, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, set := resolveConfigGitHub(config.OAuthGitHub{
		ClientID:         "cfg-cid",
		ClientSecretFile: secretPath,
		Allow:            []string{"lex"},
		Org:              "acme",
		Label:            "Cfg",
	}, quietLogger())
	if !set {
		t.Fatal("a github block with a clientID must be set")
	}
	if cfg.ClientSecret != "file-secret" {
		t.Fatalf("secret must be read from file and newline-trimmed, got %q", cfg.ClientSecret)
	}
	if cfg.ClientID != "cfg-cid" || cfg.Org != "acme" || cfg.Label != "Cfg" {
		t.Fatalf("non-secret fields not copied: %+v", cfg)
	}
}

// With no secret file, MERINO_*_CLIENT_SECRET is the fallback — so the secret
// can be an env-injected value while the rest lives in config.yml.
func TestResolveConfigOIDCSecretEnvFallback(t *testing.T) {
	t.Setenv("MERINO_OIDC_CLIENT_SECRET", "env-secret")
	cfg, set := resolveConfigOIDC(config.OAuthOIDC{
		ClientID:  "merino",
		Issuer:    "https://idp/realms/main",
		AllowRole: "herd-admin",
	}, quietLogger())
	if !set {
		t.Fatal("an oidc block with a clientID must be set")
	}
	if cfg.ClientSecret != "env-secret" {
		t.Fatalf("secret must fall back to env, got %q", cfg.ClientSecret)
	}
	if cfg.Issuer != "https://idp/realms/main" || cfg.AllowRole != "herd-admin" {
		t.Fatalf("non-secret fields not copied: %+v", cfg)
	}
}

// A named-but-missing secret file is warned about, not fatal: readSecretFile
// returns empty so the provider stays disabled rather than crashing boot.
func TestReadSecretFileMissingIsEmpty(t *testing.T) {
	if got := readSecretFile(filepath.Join(t.TempDir(), "nope"), quietLogger()); got != "" {
		t.Fatalf("missing secret file must read empty, got %q", got)
	}
	if got := readSecretFile("", quietLogger()); got != "" {
		t.Fatalf("empty path must read empty, got %q", got)
	}
}

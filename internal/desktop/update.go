package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// Updater checks GitHub Releases and installs via Wails app.Updater when wired.
//
// Check stays a light GitHub API read for the Settings sheet. Install uses the
// framework pipeline (download → SHA256 verify → unpack .app zip → swap → relaunch).
type Updater struct {
	// Owner/repo, e.g. "LoneExile/merino".
	Repo string
	// Current is the running version string (ldflags -X main.version=…).
	Current string
	// HTTP is optional; defaults to a short-timeout client (Check only).
	HTTP *http.Client
	// Framework is the live app.Updater (set after application.New). Nil in tests.
	Framework *updater.Updater
}

// UpdateInfo is what Settings renders.
type UpdateInfo struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Newer      bool   `json:"newer"`
	ReleaseURL string `json:"releaseUrl"`
	// AssetName is the zip we would install (empty if none / wrong platform).
	AssetName string `json:"assetName"`
	// CanInstall is true when Framework is wired, a zip asset exists, and Newer.
	CanInstall bool   `json:"canInstall"`
	Body       string `json:"body"`
	Published  string `json:"published"`
	CheckedAt  int64  `json:"checkedAt"`
}

// InstallResult is returned after DownloadAndInstall stages the update (before Restart).
type InstallResult struct {
	Version string `json:"version"`
	Message string `json:"message"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt string    `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// MerinoZipAssetMatcher picks Merino-*-macos-arm64.zip only.
//
// DefaultAssetMatcher looks for "darwin"+"arm64" and would select the bare
// binary merino-*-darwin-arm64 over the .app zip — which breaks bundle swap.
func MerinoZipAssetMatcher(_ updater.CheckRequest, assets []github.ReleaseAsset) int {
	// Prefer exact product zip.
	for i, a := range assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, "-macos-arm64.zip") && strings.Contains(n, "merino") {
			return i
		}
	}
	for i, a := range assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, "-macos-arm64.zip") {
			return i
		}
	}
	// Last resort: any zip with macos + arm64 (never bare binary).
	for i, a := range assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".zip") && strings.Contains(n, "macos") && strings.Contains(n, "arm64") {
			return i
		}
	}
	return -1
}

// NormalizeVersion strips leading v and turns empty into "0.0.0" for the framework.
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" || v == "dev" {
		return "0.0.0-dev"
	}
	return v
}

// Check fetches the latest non-draft GitHub release and compares tags.
func (u *Updater) Check(ctx context.Context) (UpdateInfo, error) {
	if u.Repo == "" {
		return UpdateInfo{}, fmt.Errorf("update: no repo configured")
	}
	client := u.HTTP
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	url := "https://api.github.com/repos/" + u.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return UpdateInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "merino/"+u.Current+" ("+runtime.GOOS+")")

	res, err := client.Do(req)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("update: fetch: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("update: github %s", res.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return UpdateInfo{}, fmt.Errorf("update: decode: %w", err)
	}
	if rel.Draft || rel.Prerelease {
		return UpdateInfo{Current: NormalizeVersion(u.Current), CheckedAt: time.Now().Unix()}, nil
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	current := NormalizeVersion(u.Current)
	assetName := ""
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, "-macos-arm64.zip") {
			assetName = a.Name
			break
		}
	}
	newer := versionLess(current, latest)
	return UpdateInfo{
		Current:    current,
		Latest:     latest,
		Newer:      newer,
		ReleaseURL: rel.HTMLURL,
		AssetName:  assetName,
		CanInstall: newer && assetName != "" && u.Framework != nil && runtime.GOOS == "darwin",
		Body:       trimBody(rel.Body, 800),
		Published:  rel.PublishedAt,
		CheckedAt:  time.Now().Unix(),
	}, nil
}

// Install downloads, verifies, stages, and relaunches into the latest release.
// Requires Framework (Wails app.Updater) initialized with the GitHub provider.
func (u *Updater) Install(ctx context.Context) (InstallResult, error) {
	if u.Framework == nil {
		return InstallResult{}, fmt.Errorf("update: in-app install not available in this build")
	}
	if runtime.GOOS != "darwin" {
		return InstallResult{}, fmt.Errorf("update: in-app install is macOS only")
	}

	rel, err := u.Framework.Check(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	if rel == nil {
		return InstallResult{}, fmt.Errorf("update: already on the latest version")
	}

	if err := u.Framework.DownloadAndInstall(ctx); err != nil {
		return InstallResult{}, err
	}
	// Restart swaps the staged .app and relaunches; process exits asynchronously.
	if err := u.Framework.Restart(ctx); err != nil {
		return InstallResult{}, fmt.Errorf("update: staged %s but restart failed: %w", rel.Version, err)
	}
	return InstallResult{
		Version: rel.Version,
		Message: fmt.Sprintf("Installing %s — restarting…", rel.Version),
	}, nil
}

func trimBody(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// versionLess is a tiny dotted-numeric compare (1.2.0 < 1.10.0). Non-numeric
// segments compare lexicographically. Good enough for Settings display.
func versionLess(a, b string) bool {
	// Strip prerelease for numeric compare of base; prerelease < release.
	aBase, aPre := splitPre(a)
	bBase, bPre := splitPre(b)
	as := strings.Split(aBase, ".")
	bs := strings.Split(bBase, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		var asuf, bsuf string
		if i < len(as) {
			ai, asuf = parseVerPart(as[i])
		}
		if i < len(bs) {
			bi, bsuf = parseVerPart(bs[i])
		}
		if ai != bi {
			return ai < bi
		}
		if asuf != bsuf {
			return asuf < bsuf
		}
	}
	// Same base: bare release is newer than prerelease.
	if aPre != "" && bPre == "" {
		return true
	}
	if aPre == "" && bPre != "" {
		return false
	}
	return aPre < bPre
}

func splitPre(v string) (base, pre string) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func parseVerPart(s string) (int, string) {
	n := 0
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		n = n*10 + int(s[i]-'0')
		i++
	}
	return n, s[i:]
}

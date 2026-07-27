package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// Updater checks GitHub Releases for a newer version of this app.
//
// Hand-rolled rather than Sparkle: CI already publishes a macOS binary
// artefact, and a full Sparkle feed is more machinery than a single-owner
// menubar tool needs. The Settings sheet surfaces the result; installing is
// still a one-click open of the release page (no silent binary replace).
type Updater struct {
	// Owner/repo, e.g. "LoneExile/herdr-tunnel".
	Repo string
	// Current is the running version string (build/config.yml info.version).
	Current string
	// HTTP is optional; defaults to a short-timeout client.
	HTTP *http.Client
}

// UpdateInfo is what Settings renders.
type UpdateInfo struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Newer      bool   `json:"newer"`
	ReleaseURL string `json:"releaseUrl"`
	Body       string `json:"body"`
	Published  string `json:"published"`
	CheckedAt  int64  `json:"checkedAt"`
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
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
	req.Header.Set("User-Agent", "herdr-tunnel/"+u.Current+" ("+runtime.GOOS+")")

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
		return UpdateInfo{Current: u.Current, CheckedAt: time.Now().Unix()}, nil
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(u.Current, "v")
	return UpdateInfo{
		Current:    current,
		Latest:     latest,
		Newer:      versionLess(current, latest),
		ReleaseURL: rel.HTMLURL,
		Body:       trimBody(rel.Body, 800),
		Published:  rel.PublishedAt,
		CheckedAt:  time.Now().Unix(),
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
// segments compare lexicographically. Good enough for semver-ish tags without
// pulling in a dependency.
func versionLess(a, b string) bool {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
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
	return false
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

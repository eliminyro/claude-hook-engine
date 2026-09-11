package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

// devVersion is what an unstamped build reports, and the dev guard itself: a
// binary that did not come from a release never replaces itself.
const devVersion = "dev"

// Version is the release tag, stamped into main at link time and copied here.
var Version = devVersion

// githubAPIBase is a var so tests can aim the release lookup at httptest.
var githubAPIBase = "https://api.github.com"

const (
	selfUpdateStamp = "self-update-check"
	updateTmpPrefix = ".claude-hook-engine-update-"
	apiTimeout      = 5 * time.Second
	downloadTimeout = 60 * time.Second
	smokeTimeout    = 10 * time.Second
	maxAssetBytes   = 64 << 20
)

// ghRelease is the slice of GitHub's release payload the updater reads.
type ghRelease struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r *ghRelease) assetURL(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// assetName is the release asset for the running platform.
func assetName() string {
	return fmt.Sprintf("claude-hook-engine_%s_%s", runtime.GOOS, runtime.GOARCH)
}

// selfUpdate installs the latest release over the configured binary when its
// tag differs from the running one. Every failure is a silent no-op — the hook
// runs on every session, so a broken update must never break the session.
func selfUpdate(cfg config.SelfUpdateConfig, version string) string {
	if cfg.Repo == "" || cfg.BinaryPath == "" || cfg.StateDir == "" {
		return ""
	}
	// Dev guard first: before the throttle, before any socket.
	if version == "" || version == devVersion {
		return ""
	}
	stateDir := expandHome(cfg.StateDir)
	target := expandHome(cfg.BinaryPath)
	if stateDir == "" || target == "" {
		return ""
	}
	stamp := filepath.Join(stateDir, selfUpdateStamp)
	if !dueForCheck(stamp, cfg.Interval()) {
		return ""
	}
	// Stamped before the request, so an unreachable GitHub costs one timeout
	// per interval rather than one per session.
	touchStamp(stamp)

	rel, err := latestRelease(cfg.Repo)
	if err != nil {
		slog.Debug("self-update: latest release", "repo", cfg.Repo, "error", err)
		return ""
	}
	if rel.TagName == "" || rel.TagName == version {
		return ""
	}
	if err := oldEnough(rel.PublishedAt, cfg.MinAge()); err != nil {
		slog.Debug("self-update: release age", "tag", rel.TagName, "error", err)
		return ""
	}
	if err := installRelease(target, rel); err != nil {
		slog.Debug("self-update: install", "tag", rel.TagName, "error", err)
		return ""
	}
	return fmt.Sprintf(
		"claude-hook-engine updated %s → %s. The new binary takes effect next session.", version, rel.TagName)
}

// dueForCheck reports whether the stamp file is older than the interval. A
// missing or unreadable stamp means "never checked".
func dueForCheck(stamp string, interval time.Duration) bool {
	st, err := os.Stat(stamp)
	if err != nil {
		return true
	}
	return time.Since(st.ModTime()) >= interval
}

// oldEnough gates on publication age — the window in which a bad release can
// still be withdrawn. An unreadable time fails closed: unknown is not old.
func oldEnough(publishedAt string, min time.Duration) error {
	pub, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil {
		return fmt.Errorf("unreadable published_at %q: %w", publishedAt, err)
	}
	if age := time.Since(pub); age < min {
		return fmt.Errorf("published %s ago, minimum is %s", age.Round(time.Second), min)
	}
	return nil
}

// touchStamp records that a check ran. A failure here only costs throttling.
func touchStamp(path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Debug("self-update: state dir", "error", err)
		return
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		slog.Debug("self-update: stamp", "error", err)
	}
}

func latestRelease(repo string) (*ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(githubAPIBase, "/"), repo)
	body, err := httpGet(url, apiTimeout, 1<<20)
	if err != nil {
		return nil, err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parsing release: %w", err)
	}
	return &rel, nil
}

func httpGet(url string, timeout time.Duration, limit int64) ([]byte, error) {
	resp, cancel, err := httpFetch(url, timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// httpFetch issues a bounded GET and hands back the live response; the caller
// owns both the body and the returned cancel.
func httpFetch(url string, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, cancel, nil
}

// installRelease downloads, verifies, smoke-tests and swaps in the release
// asset. The installed binary is only ever touched by the final rename.
func installRelease(target string, rel *ghRelease) error {
	name := assetName()
	binURL, sumURL := rel.assetURL(name), rel.assetURL("checksums.txt")
	if binURL == "" || sumURL == "" {
		return fmt.Errorf("release %s carries no %s plus checksums.txt", rel.TagName, name)
	}
	sums, err := httpGet(sumURL, apiTimeout, 1<<20)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	want := checksumFor(string(sums), name)
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}

	tmp, sum, err := downloadTemp(filepath.Dir(target), binURL)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", name, err)
	}
	installed := false
	defer func() {
		if !installed {
			_ = os.Remove(tmp)
		}
	}()

	if sum != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, sum, want)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := smokeTest(tmp, rel.TagName); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	installed = true
	return nil
}

// downloadTemp streams the asset into a temp file beside the target — same
// filesystem, so the later rename is atomic — and returns its SHA-256.
func downloadTemp(dir, url string) (string, string, error) {
	resp, cancel, err := httpFetch(url, downloadTimeout)
	if err != nil {
		return "", "", err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()

	f, err := os.CreateTemp(dir, updateTmpPrefix+"*")
	if err != nil {
		return "", "", err
	}
	path := f.Name()
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAssetBytes))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("writing %s: %w", path, errOr(copyErr, closeErr))
	}
	return path, hex.EncodeToString(h.Sum(nil)), nil
}

func errOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// checksumFor pulls one asset's hash out of sha256sum output.
func checksumFor(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

// smokeTest runs the downloaded file. A binary that cannot execute here — wrong
// architecture, truncated, mis-tagged — fails before it can replace a good one.
func smokeTest(path, tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("smoke test: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != tag {
		return fmt.Errorf("smoke test reported %q, want %q", got, tag)
	}
	return nil
}

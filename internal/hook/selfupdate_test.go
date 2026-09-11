package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

const testRepo = "eliminyro/claude-hook-engine"

// fakeBinary is the smoke-test fixture: a shell script that prints the tag it
// claims to be, which is exactly what `<tmp> version` has to report.
func fakeBinary(tag string) []byte {
	return []byte("#!/bin/sh\necho " + tag + "\n")
}

func checksumsFor(assets map[string][]byte) []byte {
	names := make([]string, 0, len(assets))
	for n := range assets {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		sum := sha256.Sum256(assets[n])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), n)
	}
	return []byte(b.String())
}

// publishTime is the release's publication time, swappable mid-test so one
// release can be seen young and then aged. Empty omits the field entirely.
type publishTime struct{ v atomic.Value }

func publishedAgo(age time.Duration) *publishTime {
	return publishedRaw(time.Now().Add(-age).UTC().Format(time.RFC3339))
}

func publishedRaw(s string) *publishTime {
	p := &publishTime{}
	p.set(s)
	return p
}

func (p *publishTime) set(s string) { p.v.Store(s) }
func (p *publishTime) setAgo(age time.Duration) {
	p.set(time.Now().Add(-age).UTC().Format(time.RFC3339))
}
func (p *publishTime) get() string { return p.v.Load().(string) }

// releaseServer stands in for the GitHub release API and its asset downloads,
// counting every request so a test can assert nothing was ever asked. The
// release is a month old, so the minimum-age gate is not what is under test.
func releaseServer(t *testing.T, tag string, assets map[string][]byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return releaseServerAt(t, tag, assets, publishedAgo(30*24*time.Hour))
}

func releaseServerAt(t *testing.T, tag string, assets map[string][]byte, pub *publishTime) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if name, ok := strings.CutPrefix(r.URL.Path, "/assets/"); ok {
			body, found := assets[name]
			if !found {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
			return
		}
		if r.URL.Path != "/repos/"+testRepo+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		list := make([]map[string]string, 0, len(assets))
		for name := range assets {
			list = append(list, map[string]string{
				"name": name, "browser_download_url": base + "/assets/" + name,
			})
		}
		payload := map[string]any{"tag_name": tag, "assets": list}
		if at := pub.get(); at != "" {
			payload["published_at"] = at
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	base = srv.URL
	t.Cleanup(srv.Close)
	withAPIBase(t, srv.URL)
	return srv, &hits
}

func withAPIBase(t *testing.T, base string) {
	t.Helper()
	prev := githubAPIBase
	githubAPIBase = base
	t.Cleanup(func() { githubAPIBase = prev })
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	prev := Version
	Version = v
	t.Cleanup(func() { Version = prev })
}

// installedBinary is a stand-in for ~/.local/bin/claude-hook-engine, always
// inside t.TempDir() so no test can reach the real one.
func installedBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-hook-engine")
	if err := os.WriteFile(path, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func cfgFor(target, stateDir string) config.SelfUpdateConfig {
	return config.SelfUpdateConfig{Repo: testRepo, BinaryPath: target, StateDir: stateDir}
}

// assertUnchanged proves a failed update left the installed binary alone and
// swept its temporary file away.
func assertUnchanged(t *testing.T, target string) {
	t.Helper()
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading target: %v", err)
	}
	if string(body) != "OLD" {
		t.Errorf("installed binary was replaced: %q", body)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), updateTmpPrefix) {
			t.Errorf("temporary file %s was left behind", e.Name())
		}
	}
}

func platformAssets(tag string) map[string][]byte {
	assets := map[string][]byte{assetName(): fakeBinary(tag)}
	assets["checksums.txt"] = checksumsFor(assets)
	return assets
}

func TestSelfUpdate_DevVersionMakesNoRequest(t *testing.T) {
	_, hits := releaseServer(t, "v9.9.9", platformAssets("v9.9.9"))
	target := installedBinary(t)
	state := t.TempDir()

	for _, v := range []string{devVersion, ""} {
		if got := selfUpdate(cfgFor(target, state), v); got != "" {
			t.Errorf("version %q must not update; got %q", v, got)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("a dev build made %d request(s); want 0", n)
	}
	if _, err := os.Stat(filepath.Join(state, selfUpdateStamp)); !os.IsNotExist(err) {
		t.Error("the dev guard must short-circuit before the throttle stamp is written")
	}
	assertUnchanged(t, target)
}

func TestSelfUpdate_ThrottleIsCheckedBeforeTheSocket(t *testing.T) {
	_, hits := releaseServer(t, "v2.0.0", platformAssets("v2.0.0"))
	target := installedBinary(t)
	state := t.TempDir()
	stamp := filepath.Join(state, selfUpdateStamp)
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := selfUpdate(cfgFor(target, state), "v1.0.0"); got != "" {
		t.Errorf("a fresh stamp must suppress the check; got %q", got)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("throttled check still made %d request(s); want 0", n)
	}

	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
	if got := selfUpdate(cfgFor(target, state), "v1.0.0"); got == "" {
		t.Error("a stale stamp must allow the check to run")
	}
	if hits.Load() == 0 {
		t.Error("a stale stamp made no request")
	}
	st, err := os.Stat(stamp)
	if err != nil || !st.ModTime().After(old) {
		t.Errorf("the stamp was not refreshed by the check (err=%v)", err)
	}
}

func TestSelfUpdate_EqualTagDownloadsNothing(t *testing.T) {
	_, hits := releaseServer(t, "v1.0.0", platformAssets("v1.0.0"))
	target := installedBinary(t)

	if got := selfUpdate(cfgFor(target, t.TempDir()), "v1.0.0"); got != "" {
		t.Errorf("the running tag is already latest; got %q", got)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("expected only the release lookup, got %d request(s)", n)
	}
	assertUnchanged(t, target)
}

func TestSelfUpdate_NewTagInstallsAndReports(t *testing.T) {
	releaseServer(t, "v1.1.0", platformAssets("v1.1.0"))
	target := installedBinary(t)

	notice := selfUpdate(cfgFor(target, t.TempDir()), "v1.0.0")
	if !strings.Contains(notice, "v1.1.0") || !strings.Contains(notice, "v1.0.0") {
		t.Fatalf("notice must name both versions; got %q", notice)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(fakeBinary("v1.1.0")) {
		t.Errorf("installed binary = %q, want the downloaded asset", body)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: %v", st.Mode())
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("target dir holds %d entries; the temp file should be gone", len(entries))
	}
}

func TestSelfUpdate_YoungReleaseWaitsUntilItAges(t *testing.T) {
	pub := publishedAgo(time.Minute)
	_, hits := releaseServerAt(t, "v1.1.0", platformAssets("v1.1.0"), pub)
	target := installedBinary(t)
	state := t.TempDir()
	stamp := filepath.Join(state, selfUpdateStamp)

	if got := selfUpdate(cfgFor(target, state), "v1.0.0"); got != "" {
		t.Errorf("a release younger than the minimum age must not install; got %q", got)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("the age gate must reject before any download; got %d request(s), want 1", n)
	}
	assertUnchanged(t, target)
	if _, err := os.Stat(stamp); err != nil {
		t.Errorf("the throttle stamp must still be written for a rejected release: %v", err)
	}

	// The same release, reconsidered at the next interval, has now aged past it.
	pub.setAgo(4 * time.Hour)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}

	if got := selfUpdate(cfgFor(target, state), "v1.0.0"); !strings.Contains(got, "v1.1.0") {
		t.Fatalf("an aged release must install; got %q", got)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(fakeBinary("v1.1.0")) {
		t.Errorf("installed binary = %q, want the downloaded asset", body)
	}
}

func TestSelfUpdate_UnknownPublishTimeInstallsNothing(t *testing.T) {
	for name, raw := range map[string]string{
		"absent":     "",
		"unparsable": "the day before yesterday",
	} {
		t.Run(name, func(t *testing.T) {
			_, hits := releaseServerAt(t, "v1.1.0", platformAssets("v1.1.0"), publishedRaw(raw))
			target := installedBinary(t)

			if got := selfUpdate(cfgFor(target, t.TempDir()), "v1.0.0"); got != "" {
				t.Errorf("an unestablished release age must not install; got %q", got)
			}
			if n := hits.Load(); n != 1 {
				t.Errorf("expected only the release lookup, got %d request(s)", n)
			}
			assertUnchanged(t, target)
		})
	}
}

func TestSelfUpdate_ConfiguredMinAgeReplacesTheDefault(t *testing.T) {
	releaseServerAt(t, "v1.1.0", platformAssets("v1.1.0"), publishedAgo(time.Minute))
	target := installedBinary(t)
	cfg := cfgFor(target, t.TempDir())
	cfg.MinReleaseAge = "10s"

	if got := selfUpdate(cfg, "v1.0.0"); !strings.Contains(got, "v1.1.0") {
		t.Fatalf("a release older than the configured minimum must install; got %q", got)
	}
}

func TestSelfUpdate_CorruptDownloadIsDiscarded(t *testing.T) {
	assets := map[string][]byte{
		assetName():     fakeBinary("v1.1.0"),
		"checksums.txt": []byte(strings.Repeat("a", 64) + "  " + assetName() + "\n"),
	}
	releaseServer(t, "v1.1.0", assets)
	target := installedBinary(t)

	if got := selfUpdate(cfgFor(target, t.TempDir()), "v1.0.0"); got != "" {
		t.Errorf("a checksum mismatch must not report an install; got %q", got)
	}
	assertUnchanged(t, target)
}

func TestSelfUpdate_UnrunnableDownloadIsDiscarded(t *testing.T) {
	cases := map[string][]byte{
		"reports the wrong tag": fakeBinary("v0.0.1"),
		"cannot execute":        {0x00, 0x01, 0x02, 0x03},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			assets := map[string][]byte{assetName(): payload}
			assets["checksums.txt"] = checksumsFor(assets)
			releaseServer(t, "v1.1.0", assets)
			target := installedBinary(t)

			if got := selfUpdate(cfgFor(target, t.TempDir()), "v1.0.0"); got != "" {
				t.Errorf("a failed smoke test must not report an install; got %q", got)
			}
			assertUnchanged(t, target)
		})
	}
}

func TestSelfUpdate_NoAssetForThisPlatform(t *testing.T) {
	assets := map[string][]byte{"claude-hook-engine_plan9_mips": fakeBinary("v1.1.0")}
	assets["checksums.txt"] = checksumsFor(assets)
	releaseServer(t, "v1.1.0", assets)
	target := installedBinary(t)

	if got := selfUpdate(cfgFor(target, t.TempDir()), "v1.0.0"); got != "" {
		t.Errorf("no asset for this platform must be a no-op; got %q", got)
	}
	assertUnchanged(t, target)
}

func TestSelfUpdate_BrokenAPIIsSilent(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)
	unreachable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachable.Close()

	for name, base := range map[string]string{
		"erroring api":      down.URL,
		"unreachable host":  unreachable.URL,
		"garbage json base": "://not a url",
	} {
		t.Run(name, func(t *testing.T) {
			withAPIBase(t, base)
			target := installedBinary(t)
			if got := selfUpdate(cfgFor(target, t.TempDir()), "v1.0.0"); got != "" {
				t.Errorf("a broken API must be silent; got %q", got)
			}
			assertUnchanged(t, target)
		})
	}
}

func TestSelfUpdate_AbsentConfigMakesNoRequest(t *testing.T) {
	_, hits := releaseServer(t, "v9.9.9", platformAssets("v9.9.9"))

	cases := map[string]config.SelfUpdateConfig{
		"empty block":       {},
		"no binary path":    {Repo: testRepo, StateDir: t.TempDir()},
		"no state dir":      {Repo: testRepo, BinaryPath: installedBinary(t)},
		"no repo":           {BinaryPath: installedBinary(t), StateDir: t.TempDir()},
		"unresolvable home": {Repo: testRepo, BinaryPath: "~/bin/x", StateDir: "~/state"},
	}
	t.Setenv("HOME", "")
	for name, cfg := range cases {
		if got := selfUpdate(cfg, "v1.0.0"); got != "" {
			t.Errorf("%s: expected no update, got %q", name, got)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("incomplete config made %d request(s); want 0", n)
	}
}

func TestSelfUpdateConfigInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"":      24 * time.Hour,
		"nope":  24 * time.Hour,
		"0s":    24 * time.Hour,
		"-1h":   24 * time.Hour,
		"30m":   30 * time.Minute,
		"168h":  168 * time.Hour,
		"1h30m": 90 * time.Minute,
	}
	for in, want := range cases {
		if got := (config.SelfUpdateConfig{CheckInterval: in}).Interval(); got != want {
			t.Errorf("Interval(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSelfUpdateConfigMinAge(t *testing.T) {
	cases := map[string]time.Duration{
		"":     3 * time.Hour,
		"nope": 3 * time.Hour,
		"0s":   3 * time.Hour,
		"-1h":  3 * time.Hour,
		"45m":  45 * time.Minute,
		"72h":  72 * time.Hour,
	}
	for in, want := range cases {
		if got := (config.SelfUpdateConfig{MinReleaseAge: in}).MinAge(); got != want {
			t.Errorf("MinAge(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "AABB  claude-hook-engine_linux_amd64\ncc11 *claude-hook-engine_darwin_arm64\nrubbish\n"
	if got := checksumFor(sums, "claude-hook-engine_linux_amd64"); got != "aabb" {
		t.Errorf("plain entry = %q, want aabb", got)
	}
	if got := checksumFor(sums, "claude-hook-engine_darwin_arm64"); got != "cc11" {
		t.Errorf("binary-mode entry = %q, want cc11", got)
	}
	if got := checksumFor(sums, "missing"); got != "" {
		t.Errorf("absent entry = %q, want empty", got)
	}
}

func TestHandleSessionStart_ReportsCompletedUpdate(t *testing.T) {
	releaseServer(t, "v1.1.0", platformAssets("v1.1.0"))
	withVersion(t, "v1.0.0")
	target := installedBinary(t)

	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.json")
	body, err := json.Marshal(map[string]any{
		"version": 2,
		"self_update": map[string]any{
			"repo": testRepo, "binary_path": target, "state_dir": t.TempDir(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules, body, 0o644); err != nil {
		t.Fatal(err)
	}

	ac := runSession(t, rules, "/x")
	if !strings.Contains(ac, "v1.1.0") {
		t.Errorf("session was not told about the install; additionalContext = %q", ac)
	}
}

func TestHandleSessionStart_NoSelfUpdateConfigIsSilent(t *testing.T) {
	_, hits := releaseServer(t, "v9.9.9", platformAssets("v9.9.9"))
	withVersion(t, "v1.0.0")

	rules := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(rules, []byte(`{"version":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac := runSession(t, rules, "/x"); ac != "" {
		t.Errorf("no self_update block must report nothing; got %q", ac)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("no self_update block still made %d request(s); want 0", n)
	}
}

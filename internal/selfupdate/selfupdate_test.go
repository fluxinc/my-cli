package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMain lets a re-exec'd copy of this test binary act as the self-replace
// child for TestReplaceTargetReplacesRunningExecutable: when the child env is
// set, it replaces its own running executable and exits instead of running the
// suite.
func TestMain(m *testing.M) {
	if os.Getenv(selfReplaceChildEnv) == "1" {
		self, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "child os.Executable:", err)
			os.Exit(3)
		}
		if err := replaceTargetForOS(self, []byte(selfReplacePayload), runtime.GOOS); err != nil {
			fmt.Fprintln(os.Stderr, "child replaceTargetForOS:", err)
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want int
	}{
		{"0.5.0", "0.6.0", -1},
		{"0.6.0", "0.6.0", 0},
		{"0.7.0", "0.6.0", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.2", "1.0.0-rc.1", 1},
		{"v1.2.3", "1.2.3", 0},
	}
	for _, tt := range tests {
		got, err := CompareVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.x", "1.2.3-"} {
		if _, err := CompareVersions("1.0.0", bad); err == nil {
			t.Fatalf("CompareVersions accepted malformed version %q", bad)
		}
	}
}

func TestUpdateReplacesTargetAfterChecksumVerification(t *testing.T) {
	target := writeTarget(t, "old my\n")
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new my\n"), false)
	res, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.TargetVersion != "0.2.0" {
		t.Fatalf("result = %#v", res)
	}
	if got := readFile(t, target); got != "new my\n" {
		t.Fatalf("target = %q, want new binary", got)
	}
}

func TestUpdateChecksumMismatchAbortsWithTargetUnchanged(t *testing.T) {
	target := writeTarget(t, "old my\n")
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new my\n"), true)
	_, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if got := readFile(t, target); got != "old my\n" {
		t.Fatalf("target changed after checksum mismatch: %q", got)
	}
}

func TestUpdateMissingAssetFailsWithTargetUnchanged(t *testing.T) {
	target := writeTarget(t, "old my\n")
	server := newReleaseServer(t, "0.2.0", "my-cli_0.2.0_plan9_amd64.tar.gz", []byte("new my\n"), false)
	_, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want missing asset", err)
	}
	if got := readFile(t, target); got != "old my\n" {
		t.Fatalf("target changed after missing asset: %q", got)
	}
}

func TestCheckOnlyDoesNotReplaceTarget(t *testing.T) {
	target := writeTarget(t, "old my\n")
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new my\n"), false)
	res, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		CheckOnly:      true,
		Source:         server.source(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpdateAvailable || res.Updated {
		t.Fatalf("result = %#v", res)
	}
	if got := readFile(t, target); got != "old my\n" {
		t.Fatalf("target changed during check: %q", got)
	}
	if server.assetRequests != 0 {
		t.Fatalf("asset requests = %d, want 0", server.assetRequests)
	}
}

func TestPinnedVersionCanDowngrade(t *testing.T) {
	target := writeTarget(t, "current my\n")
	server := newReleaseServer(t, "0.9.0", runtimeAsset("0.4.0"), []byte("older my\n"), false)
	res, err := Update(context.Background(), Options{
		CurrentVersion: "0.5.0",
		TargetVersion:  "0.4.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.TargetVersion != "0.4.0" {
		t.Fatalf("result = %#v", res)
	}
	if got := readFile(t, target); got != "older my\n" {
		t.Fatalf("target = %q, want pinned binary", got)
	}
}

func TestManagedInstallRefusesWithGuidance(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Cellar", "my", "0.1.0", "bin", "my")
	writeFile(t, target, "old my\n", 0o755)
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new my\n"), false)
	_, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err == nil {
		t.Fatal("expected managed install refusal")
	}
	var refusal RefusalError
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Remedy, "brew upgrade my") {
		t.Fatalf("err = %#v, want Homebrew refusal", err)
	}
	if got := readFile(t, target); got != "old my\n" {
		t.Fatalf("target changed after refusal: %q", got)
	}
}

func TestNoticeUsesFreshCacheWithoutNetwork(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if err := saveLatestVersion(cachePath, "0.2.0", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	source := Source{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected network request")
		return nil, nil
	})}}
	notice, err := CheckNotice(context.Background(), NoticeOptions{
		CurrentVersion: "0.1.0",
		CachePath:      cachePath,
		Source:         source,
		TTL:            24 * time.Hour,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !notice.UpdateAvailable || notice.LatestVersion != "0.2.0" {
		t.Fatalf("notice = %#v", notice)
	}
}

func TestNoticeZeroTTLAlwaysRefreshes(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if err := saveLatestVersion(cachePath, "0.2.0", now); err != nil {
		t.Fatal(err)
	}
	server := newReleaseServer(t, "0.3.0", runtimeAsset("0.3.0"), []byte("new my\n"), false)
	notice, err := CheckNotice(context.Background(), NoticeOptions{
		CurrentVersion: "0.1.0",
		CachePath:      cachePath,
		Source:         server.source(),
		TTL:            0,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if notice.LatestVersion != "0.3.0" {
		t.Fatalf("notice = %#v, want refreshed latest", notice)
	}
}

func TestNoticeRefreshesStaleOrCorruptCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	writeFile(t, cachePath, "{not-json", 0o644)
	server := newReleaseServer(t, "0.3.0", runtimeAsset("0.3.0"), []byte("new my\n"), false)
	notice, err := CheckNotice(context.Background(), NoticeOptions{
		CurrentVersion: "0.2.0",
		CachePath:      cachePath,
		Source:         server.source(),
		TTL:            24 * time.Hour,
		Now:            func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !notice.UpdateAvailable || notice.LatestVersion != "0.3.0" {
		t.Fatalf("notice = %#v", notice)
	}
	if got := readFile(t, cachePath); !strings.Contains(got, `"latest_version": "0.3.0"`) {
		t.Fatalf("cache = %q, want refreshed latest", got)
	}
}

func TestLatestVersionUsesReleaseRedirectWithoutAPIBaseURL(t *testing.T) {
	var apiRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/v0.4.0", http.StatusFound)
		case "/releases/tag/v0.4.0":
			_, _ = w.Write([]byte("latest release"))
		case "/api/releases/latest":
			apiRequests++
			http.Error(w, "api should not be used", http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	got, err := (Source{Client: server.Client(), DownloadBaseURL: server.URL}).LatestVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.4.0" {
		t.Fatalf("LatestVersion = %q, want 0.4.0", got)
	}
	if apiRequests != 0 {
		t.Fatalf("api requests = %d, want 0", apiRequests)
	}
}

type releaseServer struct {
	t             *testing.T
	server        *httptest.Server
	version       string
	asset         string
	archive       []byte
	checksums     []byte
	assetRequests int
}

func newReleaseServer(t *testing.T, version, asset string, binary []byte, corruptChecksum bool) *releaseServer {
	t.Helper()
	rs := &releaseServer{t: t, version: version, asset: asset}
	rs.archive = myCLIArchive(t, binary)
	sum := sha256.Sum256(rs.archive)
	if corruptChecksum {
		rs.checksums = []byte(strings.Repeat("0", sha256.Size*2) + "  " + asset + "\n")
	} else {
		rs.checksums = []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	}
	rs.server = httptest.NewServer(http.HandlerFunc(rs.handle))
	t.Cleanup(rs.server.Close)
	return rs
}

func (rs *releaseServer) source() Source {
	return Source{Client: rs.server.Client(), APIBaseURL: rs.server.URL, DownloadBaseURL: rs.server.URL}
}

func (rs *releaseServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/releases/latest":
		_, _ = fmt.Fprintf(w, `{"tag_name":"v%s"}`, rs.version)
	default:
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			_, _ = w.Write(rs.checksums)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/"+rs.asset) {
			rs.assetRequests++
			_, _ = w.Write(rs.archive)
			return
		}
		http.NotFound(w, r)
	}
}

func runtimeAsset(version string) string {
	return assetName(version, runtime.GOOS, runtime.GOARCH)
}

func myCLIArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "my", Mode: 0o755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func myCLIArchiveNamed(t *testing.T, name string, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractAcceptsWindowsExeName(t *testing.T) {
	want := []byte("windows-my-binary")
	got, err := extractMyCLIBinary(myCLIArchiveNamed(t, "my.exe", want))
	if err != nil {
		t.Fatalf("extractMyCLIBinary(my.exe): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted %q, want %q", got, want)
	}
}

// TestReplaceTargetWindowsBranchSwapsBinary exercises the Windows code path's
// rename-aside logic on any OS (it does not replace a *running* exe — see
// TestReplaceTargetReplacesRunningExecutable for the live-lock proof).
func TestReplaceTargetWindowsBranchSwapsBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "my.exe")
	writeFile(t, target, "old-binary", 0o755)
	// A stale backup from a prior update must not block the replacement.
	writeFile(t, target+".old", "stale", 0o755)

	if err := replaceTargetForOS(target, []byte("new-binary"), "windows"); err != nil {
		t.Fatalf("replaceTargetForOS windows: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "new-binary" {
		t.Fatalf("target = %q err=%v, want new-binary", got, err)
	}
}

const selfReplaceChildEnv = "MYCLI_SELFUPDATE_REPLACE_CHILD"
const selfReplacePayload = "running-exe-replacement"

// TestReplaceTargetReplacesRunningExecutable proves the real case that matters
// on Windows: replacing the executable file that backs the *currently running*
// process. The test re-execs a copy of its own binary, which (via TestMain)
// calls replaceTargetForOS on its own running executable. It is skipped off
// Windows, where overwriting a running binary is not locked.
func TestReplaceTargetReplacesRunningExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("running-exe lock semantics are Windows-specific")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "my.exe")
	if err := os.WriteFile(target, src, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(target)
	cmd.Env = append(os.Environ(), selfReplaceChildEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-replace child failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != selfReplacePayload {
		t.Fatalf("running executable was not replaced: got %q", string(got))
	}
}

func writeTarget(t *testing.T, body string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "my")
	writeFile(t, target, body, 0o755)
	return target
}

func writeFile(t *testing.T, path, body string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
	target := writeTarget(t, "old flux\n")
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new flux\n"), false)
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
	if got := readFile(t, target); got != "new flux\n" {
		t.Fatalf("target = %q, want new binary", got)
	}
}

func TestUpdateChecksumMismatchAbortsWithTargetUnchanged(t *testing.T) {
	target := writeTarget(t, "old flux\n")
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new flux\n"), true)
	_, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if got := readFile(t, target); got != "old flux\n" {
		t.Fatalf("target changed after checksum mismatch: %q", got)
	}
}

func TestUpdateMissingAssetFailsWithTargetUnchanged(t *testing.T) {
	target := writeTarget(t, "old flux\n")
	server := newReleaseServer(t, "0.2.0", "flux_0.2.0_plan9_amd64.tar.gz", []byte("new flux\n"), false)
	_, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want missing asset", err)
	}
	if got := readFile(t, target); got != "old flux\n" {
		t.Fatalf("target changed after missing asset: %q", got)
	}
}

func TestCheckOnlyDoesNotReplaceTarget(t *testing.T) {
	target := writeTarget(t, "old flux\n")
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new flux\n"), false)
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
	if got := readFile(t, target); got != "old flux\n" {
		t.Fatalf("target changed during check: %q", got)
	}
	if server.assetRequests != 0 {
		t.Fatalf("asset requests = %d, want 0", server.assetRequests)
	}
}

func TestPinnedVersionCanDowngrade(t *testing.T) {
	target := writeTarget(t, "current flux\n")
	server := newReleaseServer(t, "0.9.0", runtimeAsset("0.4.0"), []byte("older flux\n"), false)
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
	if got := readFile(t, target); got != "older flux\n" {
		t.Fatalf("target = %q, want pinned binary", got)
	}
}

func TestManagedInstallRefusesWithGuidance(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Cellar", "flux", "0.1.0", "bin", "flux")
	writeFile(t, target, "old flux\n", 0o755)
	server := newReleaseServer(t, "0.2.0", runtimeAsset("0.2.0"), []byte("new flux\n"), false)
	_, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		TargetPath:     target,
		Source:         server.source(),
	})
	if err == nil {
		t.Fatal("expected managed install refusal")
	}
	var refusal RefusalError
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Remedy, "brew upgrade flux") {
		t.Fatalf("err = %#v, want Homebrew refusal", err)
	}
	if got := readFile(t, target); got != "old flux\n" {
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
	server := newReleaseServer(t, "0.3.0", runtimeAsset("0.3.0"), []byte("new flux\n"), false)
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
	server := newReleaseServer(t, "0.3.0", runtimeAsset("0.3.0"), []byte("new flux\n"), false)
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
	rs.archive = fluxArchive(t, binary)
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

func fluxArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "flux", Mode: 0o755, Size: int64(len(binary))}); err != nil {
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

func writeTarget(t *testing.T, body string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "flux")
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

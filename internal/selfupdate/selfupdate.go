// Package selfupdate implements release-backed my binary updates.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL      = "https://api.github.com/repos/fluxinc/my-cli"
	defaultDownloadBaseURL = "https://github.com/fluxinc/my-cli"
	DefaultCheckTTL        = 24 * time.Hour
	cacheSchemaVersion     = 1
	cacheFileName          = "update-check.json"
)

// Source fetches My AI GitHub release metadata and assets.
type Source struct {
	Client          *http.Client
	APIBaseURL      string
	DownloadBaseURL string
}

// Options controls a self-update or update check.
type Options struct {
	CurrentVersion string
	TargetVersion  string
	CheckOnly      bool
	TargetPath     string
	Home           string
	Source         Source
	GOOS           string
	GOARCH         string
}

// Result describes an update command outcome.
type Result struct {
	CurrentVersion  string `json:"current_version"`
	TargetVersion   string `json:"target_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	TargetPath      string `json:"target_path,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Updated         bool   `json:"updated"`
	CheckOnly       bool   `json:"check_only,omitempty"`
	Message         string `json:"message"`
}

// NoticeOptions controls the best-effort automatic release notice.
type NoticeOptions struct {
	CurrentVersion string
	Home           string
	CachePath      string
	Source         Source
	TTL            time.Duration
	Now            func() time.Time
}

// Notice reports whether a newer release should be shown to the user.
type Notice struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	CachePath       string `json:"cache_path,omitempty"`
}

type latestReleasePayload struct {
	TagName string `json:"tag_name"`
}

type updateCheckCache struct {
	SchemaVersion int    `json:"schema_version"`
	LastChecked   string `json:"last_checked"`
	LatestVersion string `json:"latest_version"`
}

type semver struct {
	major int
	minor int
	patch int
	pre   string
}

// RefusalError is returned when the CLI detects an install it should not replace.
type RefusalError struct {
	Reason string
	Remedy string
}

func (e RefusalError) Error() string {
	if e.Remedy == "" {
		return e.Reason
	}
	return e.Reason + "; " + e.Remedy
}

// Check resolves the target version and reports whether an update would happen.
func Check(ctx context.Context, opts Options) (Result, error) {
	current, err := NormalizeVersion(opts.CurrentVersion)
	if err != nil {
		return Result{}, fmt.Errorf("current version: %w", err)
	}
	target := strings.TrimSpace(opts.TargetVersion)
	pinned := target != ""
	latest := ""
	if target == "" {
		latest, err = opts.Source.LatestVersion(ctx)
		if err != nil {
			return Result{}, err
		}
		target = latest
	} else {
		target, err = NormalizeVersion(target)
		if err != nil {
			return Result{}, fmt.Errorf("target version: %w", err)
		}
	}
	cmp, err := CompareVersions(current, target)
	if err != nil {
		return Result{}, err
	}
	updateAvailable := cmp < 0
	if pinned {
		updateAvailable = cmp != 0
	}
	res := Result{
		CurrentVersion:  current,
		TargetVersion:   target,
		LatestVersion:   latest,
		UpdateAvailable: updateAvailable,
		CheckOnly:       opts.CheckOnly,
		Message:         checkMessage(current, target, latest, pinned, updateAvailable),
	}
	return res, nil
}

// Update replaces the my binary with the requested release, unless CheckOnly is set.
func Update(ctx context.Context, opts Options) (Result, error) {
	res, err := Check(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	if opts.CheckOnly || !res.UpdateAvailable {
		return res, nil
	}
	targetPath, err := executableTarget(opts.TargetPath)
	if err != nil {
		return Result{}, err
	}
	res.TargetPath = targetPath
	if err := GuardInstallTarget(targetPath, opts.Home); err != nil {
		return Result{}, err
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	asset := assetName(res.TargetVersion, goos, goarch)
	tag := releaseTag(res.TargetVersion)
	archiveData, err := opts.Source.DownloadAsset(ctx, tag, asset)
	if err != nil {
		return Result{}, err
	}
	checksumData, err := opts.Source.DownloadAsset(ctx, tag, "checksums.txt")
	if err != nil {
		return Result{}, err
	}
	if err := verifyChecksum(asset, archiveData, checksumData); err != nil {
		return Result{}, err
	}
	binary, err := extractMyCLIBinary(archiveData)
	if err != nil {
		return Result{}, err
	}
	if err := replaceTarget(targetPath, binary); err != nil {
		return Result{}, err
	}
	res.Updated = true
	res.Message = fmt.Sprintf("updated my %s -> %s", res.CurrentVersion, res.TargetVersion)
	return res, nil
}

// CheckNotice returns a best-effort notice result, using a user-level TTL cache.
func CheckNotice(ctx context.Context, opts NoticeOptions) (Notice, error) {
	current, err := NormalizeVersion(opts.CurrentVersion)
	if err != nil {
		return Notice{}, fmt.Errorf("current version: %w", err)
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	ttl := opts.TTL
	cachePath := opts.CachePath
	if cachePath == "" {
		cachePath, err = CachePath(opts.Home)
		if err != nil {
			return Notice{}, err
		}
	}
	if latest, ok := cachedLatestVersion(cachePath, now, ttl); ok {
		return noticeForVersions(current, latest, cachePath)
	}
	latest, err := opts.Source.LatestVersion(ctx)
	if err != nil {
		return Notice{}, err
	}
	_ = saveLatestVersion(cachePath, latest, now)
	return noticeForVersions(current, latest, cachePath)
}

// CachePath returns the per-user update check cache path.
func CachePath(homeOverride string) (string, error) {
	home, err := resolveHome(homeOverride)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "my-cli", cacheFileName), nil
}

// UpdateCheckTTLFromEnv parses MYCLI_UPDATE_CHECK_TTL.
func UpdateCheckTTLFromEnv() time.Duration {
	value := strings.TrimSpace(os.Getenv("MYCLI_UPDATE_CHECK_TTL"))
	if value == "" {
		return DefaultCheckTTL
	}
	ttl, err := time.ParseDuration(value)
	if err != nil {
		return DefaultCheckTTL
	}
	return ttl
}

// NormalizeVersion accepts release tags or versions and returns X.Y.Z[-pre].
func NormalizeVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	if value == "" {
		return "", fmt.Errorf("empty version")
	}
	if _, err := parseSemver(value); err != nil {
		return "", err
	}
	return value, nil
}

// CompareVersions compares two semantic versions. It returns -1, 0, or 1.
func CompareVersions(a, b string) (int, error) {
	av, err := parseSemver(strings.TrimPrefix(strings.TrimSpace(a), "v"))
	if err != nil {
		return 0, fmt.Errorf("%q: %w", a, err)
	}
	bv, err := parseSemver(strings.TrimPrefix(strings.TrimSpace(b), "v"))
	if err != nil {
		return 0, fmt.Errorf("%q: %w", b, err)
	}
	for _, pair := range [][2]int{{av.major, bv.major}, {av.minor, bv.minor}, {av.patch, bv.patch}} {
		if pair[0] < pair[1] {
			return -1, nil
		}
		if pair[0] > pair[1] {
			return 1, nil
		}
	}
	if av.pre == bv.pre {
		return 0, nil
	}
	if av.pre == "" {
		return 1, nil
	}
	if bv.pre == "" {
		return -1, nil
	}
	if av.pre < bv.pre {
		return -1, nil
	}
	return 1, nil
}

// LatestVersion fetches the latest GitHub release tag.
func (s Source) LatestVersion(ctx context.Context) (string, error) {
	data, err := s.get(ctx, strings.TrimRight(s.apiBaseURL(), "/")+"/releases/latest", 3*time.Second)
	if err != nil {
		return "", err
	}
	var payload latestReleasePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("parse latest release: %w", err)
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("latest release response is missing tag_name")
	}
	return NormalizeVersion(payload.TagName)
}

// DownloadAsset downloads a release asset by tag and filename.
func (s Source) DownloadAsset(ctx context.Context, tag, name string) ([]byte, error) {
	u := strings.TrimRight(s.downloadBaseURL(), "/") + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
	return s.get(ctx, u, 60*time.Second)
}

func (s Source) get(ctx context.Context, rawURL string, timeout time.Duration) ([]byte, error) {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "my-self-update")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("GET %s: %s", rawURL, message)
	}
	return io.ReadAll(resp.Body)
}

func (s Source) apiBaseURL() string {
	if strings.TrimSpace(s.APIBaseURL) != "" {
		return strings.TrimSpace(s.APIBaseURL)
	}
	return defaultAPIBaseURL
}

func (s Source) downloadBaseURL() string {
	if strings.TrimSpace(s.DownloadBaseURL) != "" {
		return strings.TrimSpace(s.DownloadBaseURL)
	}
	return defaultDownloadBaseURL
}

func parseSemver(value string) (semver, error) {
	main := value
	if before, _, ok := strings.Cut(main, "+"); ok {
		main = before
	}
	pre := ""
	if before, after, ok := strings.Cut(main, "-"); ok {
		main = before
		pre = after
		if pre == "" {
			return semver{}, fmt.Errorf("malformed version")
		}
	}
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("malformed version")
	}
	nums := make([]int, 3)
	for i, part := range parts {
		if part == "" {
			return semver{}, fmt.Errorf("malformed version")
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("malformed version")
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}, nil
}

func checkMessage(current, target, latest string, pinned, updateAvailable bool) string {
	if !updateAvailable {
		if pinned {
			return fmt.Sprintf("already at requested my version %s", current)
		}
		return fmt.Sprintf("already up to date (%s)", current)
	}
	if pinned {
		return fmt.Sprintf("my %s can be replaced with requested version %s", current, target)
	}
	if latest == "" {
		latest = target
	}
	return fmt.Sprintf("newer my %s is available (current %s)", latest, current)
}

func executableTarget(explicit string) (string, error) {
	target := explicit
	if target == "" {
		path, err := os.Executable()
		if err != nil {
			return "", err
		}
		target = path
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// GuardInstallTarget refuses installs that should be updated by their manager.
func GuardInstallTarget(target, home string) error {
	method := managedInstallMethod(target, home)
	switch method {
	case "homebrew":
		return RefusalError{
			Reason: "my appears to be managed by Homebrew",
			Remedy: "update with your package manager: brew upgrade my",
		}
	case "go":
		return RefusalError{
			Reason: "my appears to be installed by go install",
			Remedy: "update with: go install github.com/fluxinc/my-cli/cmd/my@latest",
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o222 == 0 {
		return RefusalError{
			Reason: "my binary is not writable",
			Remedy: "re-run install.sh or set MYCLI_INSTALL_DIR to a writable directory",
		}
	}
	if err := checkDirWritable(filepath.Dir(target)); err != nil {
		return RefusalError{
			Reason: "my install directory is not writable",
			Remedy: "re-run install.sh or set MYCLI_INSTALL_DIR to a writable directory",
		}
	}
	return nil
}

func managedInstallMethod(target, home string) string {
	clean := filepath.Clean(target)
	slash := filepath.ToSlash(clean)
	if strings.Contains(slash, "/Cellar/") {
		return "homebrew"
	}
	if prefix := brewPrefix(); prefix != "" && pathWithin(clean, prefix) {
		return "homebrew"
	}
	for _, root := range goBinRoots(home) {
		if pathWithin(clean, root) {
			return "go"
		}
	}
	return ""
}

func brewPrefix() string {
	path, err := exec.LookPath("brew")
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, "--prefix").Output()
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return ""
	}
	abs, err := filepath.Abs(prefix)
	if err != nil {
		return ""
	}
	return abs
}

func goBinRoots(home string) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		clean := filepath.Clean(abs)
		if !seen[clean] {
			seen[clean] = true
			roots = append(roots, clean)
		}
	}
	add(os.Getenv("GOBIN"))
	for _, gp := range filepath.SplitList(os.Getenv("GOPATH")) {
		add(filepath.Join(gp, "bin"))
	}
	if resolvedHome, err := resolveHome(home); err == nil {
		add(filepath.Join(resolvedHome, "go", "bin"))
	}
	if path, err := exec.LookPath("go"); err == nil {
		out, err := exec.Command(path, "env", "GOBIN", "GOPATH").Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) > 0 {
				add(lines[0])
			}
			if len(lines) > 1 {
				for _, gp := range filepath.SplitList(lines[1]) {
					add(filepath.Join(gp, "bin"))
				}
			}
		}
	}
	return roots
}

func pathWithin(path, root string) bool {
	if root == "" {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs = filepath.Clean(pathAbs)
	rootAbs = filepath.Clean(rootAbs)
	if pathAbs == rootAbs {
		return true
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkDirWritable(dir string) error {
	tmp, err := os.CreateTemp(dir, ".my-cli-write-check-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	closeErr := tmp.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func assetName(version, goos, goarch string) string {
	return fmt.Sprintf("my-cli_%s_%s_%s.tar.gz", version, goos, goarch)
}

func releaseTag(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func verifyChecksum(asset string, archiveData, checksumData []byte) error {
	want, err := checksumForAsset(asset, checksumData)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archiveData)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

func checksumForAsset(asset string, data []byte) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name != asset && filepath.Base(name) != asset {
			continue
		}
		sum := fields[0]
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("malformed checksum for %s", asset)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("malformed checksum for %s", asset)
		}
		return sum, nil
	}
	return "", fmt.Errorf("checksums.txt does not contain %s", asset)
}

func extractMyCLIBinary(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive does not contain my binary")
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if base := filepath.Base(header.Name); base != "my" && base != "my.exe" {
			continue
		}
		var out bytes.Buffer
		if _, err := io.Copy(&out, tr); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
}

func replaceTarget(target string, binary []byte) error {
	return replaceTargetForOS(target, binary, runtime.GOOS)
}

// replaceTargetForOS writes binary over target by staging a temp file in the
// same directory and renaming it into place. On Windows a running executable is
// locked and cannot be renamed over or deleted, so the current binary is first
// moved aside (which Windows permits even while it runs); the moved-aside file
// keeps backing the running process and is cleaned up on a later update.
func replaceTargetForOS(target string, binary []byte, goos string) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".my-cli-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if goos == "windows" {
		backup := target + ".old"
		_ = os.Remove(backup) // clear a stale backup from a prior update
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		if err := os.Rename(tmpName, target); err != nil {
			_ = os.Rename(backup, target) // best-effort restore
			return err
		}
		keep = true
		_ = os.Remove(backup) // best effort; fails while the old exe is running
		return nil
	}

	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	keep = true
	return nil
}

func cachedLatestVersion(path string, now time.Time, ttl time.Duration) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var cache updateCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return "", false
	}
	latest, err := NormalizeVersion(cache.LatestVersion)
	if err != nil {
		return "", false
	}
	checked, err := time.Parse(time.RFC3339, cache.LastChecked)
	if err != nil {
		return "", false
	}
	if ttl > 0 && now.Sub(checked) < ttl {
		return latest, true
	}
	return "", false
}

func saveLatestVersion(path, latest string, now time.Time) error {
	latest, err := NormalizeVersion(latest)
	if err != nil {
		return err
	}
	cache := updateCheckCache{
		SchemaVersion: cacheSchemaVersion,
		LastChecked:   now.UTC().Format(time.RFC3339),
		LatestVersion: latest,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func noticeForVersions(current, latest, cachePath string) (Notice, error) {
	latest, err := NormalizeVersion(latest)
	if err != nil {
		return Notice{}, err
	}
	cmp, err := CompareVersions(current, latest)
	if err != nil {
		return Notice{}, err
	}
	return Notice{
		CurrentVersion:  current,
		LatestVersion:   latest,
		UpdateAvailable: cmp < 0,
		CachePath:       cachePath,
	}, nil
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}

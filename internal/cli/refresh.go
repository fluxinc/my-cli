package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

const autoRefreshFile = "auto-refresh.json"

const defaultRefreshTTL = 6 * time.Hour

type autoRefreshState struct {
	SchemaVersion int                          `json:"schema_version"`
	Repos         map[string]autoRefreshRecord `json:"repos,omitempty"`
}

type autoRefreshRecord struct {
	LastAutoRefresh string `json:"last_auto_refresh"`
}

func existingUmbrellaRoot(home, manifestName, explicit string) (string, bool, error) {
	if explicit != "" {
		root, err := resolveUmbrellaRoot(home, explicit)
		if err != nil {
			return "", false, err
		}
		return existingUmbrellaRootStatus(root, manifestName)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return existingUmbrellaRootStatus(root, manifestName)
	}
	root, err := resolveMyRoot(home, manifestName, "")
	if err != nil {
		return "", false, nil
	}
	return existingUmbrellaRootStatus(root, manifestName)
}

type umbrellaManifestMismatchError struct {
	Root string
	Have string
	Want string
}

func (e umbrellaManifestMismatchError) Error() string {
	return fmt.Sprintf("umbrella %s uses manifest %q, not %q", e.Root, e.Have, e.Want)
}

func existingUmbrellaRootStatus(root, manifestName string) (string, bool, error) {
	if !hasMyDir(root) {
		return root, false, nil
	}
	if manifestName == "" {
		return root, true, nil
	}
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		return root, false, err
	}
	if ws.ManifestRef != "" && ws.ManifestRef != manifestName {
		return root, false, umbrellaManifestMismatchError{
			Root: root,
			Have: ws.ManifestRef,
			Want: manifestName,
		}
	}
	return root, true, nil
}

func hasMyDir(root string) bool {
	info, err := os.Stat(filepath.Join(root, umbrella.DirName))
	return err == nil && info.IsDir()
}

func (a app) maybeAutoRefresh(home, manifestName, umbrellaRoot, root string, noRefresh bool) {
	if noRefresh || os.Getenv("MYCLI_NO_AUTO_REFRESH") != "" || root == "" || !hasMyDir(root) {
		return
	}
	ttl := autoRefreshTTL()
	state, err := loadAutoRefreshState(root)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: auto-refresh skipped: %v\n", err)
		return
	}
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "local")
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: auto-refresh skipped: %v\n", err)
		return
	}
	now := time.Now().UTC()
	changed := false
	for _, entry := range entries {
		if !autoRefreshEntryAllowed(entry) {
			continue
		}
		key := autoRefreshKey(entry)
		if !autoRefreshDue(state, key, now, ttl) {
			continue
		}
		result := syncer.Inspect([]syncer.Entry{entry}, syncer.InspectOptions{Fetch: true})[0]
		item, _, include := doctorFixFreshnessItem(entry, result)
		state.Repos[key] = autoRefreshRecord{LastAutoRefresh: now.Format(time.RFC3339)}
		changed = true
		if include && item.Status == "fixed" {
			fmt.Fprintf(a.stderr, "refresh\t%s\tfixed\t%s\n", item.Name, item.Message)
		} else if include && item.Status == "error" {
			fmt.Fprintf(a.stderr, "warning: auto-refresh %s failed: %s\n", item.Name, item.Message)
		} else if notice, ok := freshnessNotice(result); ok {
			fmt.Fprintf(a.stderr, "notice\t%s\t%s\n", doctorFreshnessName(result), notice)
		}
	}
	if changed {
		if err := saveAutoRefreshState(root, state); err != nil {
			fmt.Fprintf(a.stderr, "warning: auto-refresh state not saved: %v\n", err)
		}
	}
}

// freshnessNotice describes a checkout the auto-refresh could not converge,
// with the command that reconciles it. Failed and unknown inspections are
// reported elsewhere; converged checkouts return false.

// freshnessNotice describes a checkout the auto-refresh could not converge,
// with the command that reconciles it. Failed and unknown inspections are
// reported elsewhere; converged checkouts return false.
func freshnessNotice(result syncer.Result) (string, bool) {
	if result.Status == "failed" || result.Status == "unknown" || result.BehindUnknown {
		return "", false
	}
	dirty := len(result.Dirty)
	switch {
	case result.Ahead > 0 && result.Behind > 0:
		return fmt.Sprintf("diverged from remote (ahead %d, behind %d); run `my doctor` and reconcile manually", result.Ahead, result.Behind), true
	case dirty > 0 && result.Behind > 0:
		return fmt.Sprintf("behind remote by %d with %d uncommitted file(s); commit or stash, then run `my sync`", result.Behind, dirty), true
	case dirty > 0:
		return fmt.Sprintf("%d uncommitted file(s); run `my sync --push --print` to review publish work", dirty), true
	case result.Ahead > 0:
		return fmt.Sprintf("ahead of remote by %d unpublished commit(s); run `my sync --push` to publish", result.Ahead), true
	case result.Behind > 0:
		return fmt.Sprintf("behind remote by %d; run `my sync`", result.Behind), true
	}
	return "", false
}

func autoRefreshTTL() time.Duration {
	value := strings.TrimSpace(os.Getenv("MYCLI_REFRESH_TTL"))
	if value == "" {
		return defaultRefreshTTL
	}
	ttl, err := time.ParseDuration(value)
	if err != nil {
		return defaultRefreshTTL
	}
	return ttl
}

func loadAutoRefreshState(root string) (autoRefreshState, error) {
	path := filepath.Join(root, umbrella.DirName, autoRefreshFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return autoRefreshState{SchemaVersion: 1, Repos: map[string]autoRefreshRecord{}}, nil
	}
	if err != nil {
		return autoRefreshState{}, err
	}
	var state autoRefreshState
	if err := json.Unmarshal(data, &state); err != nil {
		return autoRefreshState{}, fmt.Errorf("read %s: %w", path, err)
	}
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.Repos == nil {
		state.Repos = map[string]autoRefreshRecord{}
	}
	return state, nil
}

func saveAutoRefreshState(root string, state autoRefreshState) error {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.Repos == nil {
		state.Repos = map[string]autoRefreshRecord{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(root, umbrella.DirName, autoRefreshFile), data, 0o644)
}

func autoRefreshEntryAllowed(entry syncer.Entry) bool {
	return entry.Role == "manifest" || entry.Role == "content"
}

func autoRefreshKey(entry syncer.Entry) string {
	manifestName := entry.Manifest
	if manifestName == "" {
		manifestName = entry.ID
	}
	return entry.Role + ":" + manifestName + ":" + entry.ID
}

func autoRefreshDue(state autoRefreshState, key string, now time.Time, ttl time.Duration) bool {
	record, ok := state.Repos[key]
	if !ok || record.LastAutoRefresh == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, record.LastAutoRefresh)
	if err != nil {
		return true
	}
	return !last.Add(ttl).After(now)
}

func findGnitWorkspaceRoot(start string) string {
	for dir := filepath.Clean(start); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".gnit", "roster.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}

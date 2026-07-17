package access

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const quarantineJournalSchemaVersion = 1

type QuarantineInput struct {
	Home      string
	Path      string
	ActorID   int64
	Now       time.Time
	Failpoint func(string) error
	Rename    func(string, string) error
}

type QuarantineJournal struct {
	SchemaVersion int                `json:"schema_version"`
	Phase         string             `json:"phase"`
	CreatedAt     string             `json:"created_at"`
	CompletedAt   string             `json:"completed_at,omitempty"`
	Repository    Repository         `json:"repository"`
	Actor         Actor              `json:"actor"`
	ReasonCode    string             `json:"reason_code"`
	InventoryPath string             `json:"inventory_path"`
	Sources       []QuarantineSource `json:"sources"`
}

type QuarantineSource struct {
	OriginalPath     string   `json:"original_path"`
	QuarantinedPath  string   `json:"quarantined_path"`
	CapsulePath      string   `json:"capsule_path"`
	State            string   `json:"state"`
	PurgeEligible    bool     `json:"purge_eligible"`
	RetentionReasons []string `json:"retention_reasons"`
}

type QuarantineResult struct {
	JournalPath string            `json:"journal_path"`
	Journal     QuarantineJournal `json:"journal"`
}

// QuarantineConfirmed losslessly moves a positively baselined checkout only
// after RecordObservation has persisted confirmed revocation evidence. It has
// no copy or delete fallback: any preparation or rename failure leaves the
// remaining source path in place.
func QuarantineConfirmed(input QuarantineInput) (QuarantineResult, error) {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	inventory, entry, progress, err := confirmedInventoryEntry(input.Home, input.Path, input.ActorID)
	if err != nil {
		return QuarantineResult{}, err
	}
	lockPath, release, err := acquireQuarantineLock(entry.AllowedRoot)
	if err != nil {
		return QuarantineResult{}, err
	}
	defer release()
	_ = lockPath

	sources, err := discoverQuarantineSources(entry)
	if err != nil {
		return QuarantineResult{}, err
	}
	root := filepath.Join(entry.AllowedRoot, ".my-cli", "quarantine")
	if withinOrEqualPath(entry.CanonicalPath, root) {
		return QuarantineResult{}, fmt.Errorf("quarantine root must be outside managed checkout")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return QuarantineResult{}, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return QuarantineResult{}, err
	}
	if err := secureQuarantineDirectory(root); err != nil {
		return QuarantineResult{}, err
	}
	prefix := now.Format("20060102T150405Z") + "-" + safeQuarantineName(entry.Repository.NodeID) + "-"
	transactionDir, err := os.MkdirTemp(root, prefix)
	if err != nil {
		return QuarantineResult{}, err
	}
	if err := os.Chmod(transactionDir, 0o700); err != nil {
		return QuarantineResult{}, err
	}
	if err := secureQuarantineDirectory(transactionDir); err != nil {
		return QuarantineResult{}, err
	}
	journalPath := filepath.Join(transactionDir, "journal.json")
	journal := QuarantineJournal{
		SchemaVersion: quarantineJournalSchemaVersion, Phase: "preparing",
		CreatedAt: now.Format(time.RFC3339Nano), Repository: entry.Repository,
		Actor: progress.Actor, ReasonCode: progress.ReasonCode, InventoryPath: entry.CanonicalPath,
	}
	for i, source := range sources {
		journal.Sources = append(journal.Sources, QuarantineSource{
			OriginalPath: source, QuarantinedPath: filepath.Join(transactionDir, fmt.Sprintf("checkout-%03d", i)),
			CapsulePath: filepath.Join(transactionDir, "capsules", fmt.Sprintf("%03d", i)), State: "preparing",
		})
	}
	if err := saveQuarantineJournal(journalPath, journal); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	for i := range journal.Sources {
		source := &journal.Sources[i]
		capsule, err := BuildRecoveryCapsule(source.OriginalPath, source.CapsulePath, entry.Repository, now)
		if err != nil {
			return QuarantineResult{JournalPath: journalPath, Journal: journal}, fmt.Errorf("build recovery capsule for %s: %w", source.OriginalPath, err)
		}
		source.State = "prepared"
		source.PurgeEligible = capsule.PurgeEligible
		source.RetentionReasons = append([]string(nil), capsule.RetentionReasons...)
		if err := saveQuarantineJournal(journalPath, journal); err != nil {
			return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
		}
	}
	journal.Phase = "intent"
	if err := saveQuarantineJournal(journalPath, journal); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	if err := runQuarantineFailpoint(input.Failpoint, "after-intent"); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	if err := validateQuarantineJournal(journalPath, journal, entry.AllowedRoot); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	journal, err = advanceQuarantineMoves(journalPath, journal, input.Failpoint, input.Rename)
	if err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	inventory, err = LoadInventory(input.Home)
	if err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	if err := finalizeQuarantineInventory(input.Home, inventory, journalPath, journal); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	if err := appendQuarantineSecurityEvent(input.Home, journalPath, journal); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	journal.Phase = "complete"
	journal.CompletedAt = now.Format(time.RFC3339Nano)
	if err := saveQuarantineJournal(journalPath, journal); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	return QuarantineResult{JournalPath: journalPath, Journal: journal}, nil
}

// ResumeQuarantine converges an interrupted intent based only on the journal
// and exact source/destination existence. It is safe after a crash between
// rename and journal persistence.
func ResumeQuarantine(home, journalPath string, failpoint func(string) error) (QuarantineResult, error) {
	journal, err := loadQuarantineJournal(journalPath)
	if err != nil {
		return QuarantineResult{}, err
	}
	inventory, err := LoadInventory(home)
	if err != nil {
		return QuarantineResult{}, err
	}
	entryRoot, err := allowedRootForJournal(inventory, journalPath, journal)
	if err != nil {
		return QuarantineResult{}, err
	}
	if err := validateQuarantineJournal(journalPath, journal, entryRoot); err != nil {
		return QuarantineResult{}, err
	}
	_, release, err := acquireQuarantineLock(entryRoot)
	if err != nil {
		return QuarantineResult{}, err
	}
	defer release()
	inventory, err = LoadInventory(home)
	if err != nil {
		return QuarantineResult{}, err
	}
	if _, err := allowedRootForJournal(inventory, journalPath, journal); err != nil {
		return QuarantineResult{}, err
	}
	if journal.Phase != "complete" {
		journal, err = advanceQuarantineMoves(journalPath, journal, failpoint, nil)
		if err != nil {
			return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
		}
	}
	if err := finalizeQuarantineInventory(home, inventory, journalPath, journal); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	if err := appendQuarantineSecurityEvent(home, journalPath, journal); err != nil {
		return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
	}
	if journal.Phase != "complete" {
		journal.Phase = "complete"
		journal.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := saveQuarantineJournal(journalPath, journal); err != nil {
			return QuarantineResult{JournalPath: journalPath, Journal: journal}, err
		}
	}
	return QuarantineResult{JournalPath: journalPath, Journal: journal}, nil
}

func confirmedInventoryEntry(home, path string, actorID int64) (Inventory, ManagedRepository, RevocationProgress, error) {
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return Inventory{}, ManagedRepository{}, RevocationProgress{}, err
	}
	inventory, err := LoadInventory(home)
	if err != nil {
		return Inventory{}, ManagedRepository{}, RevocationProgress{}, err
	}
	for _, entry := range inventory.Repositories {
		if entry.CanonicalPath != canonical {
			continue
		}
		if rebound, root, err := canonicalManagedPath(entry.CanonicalPath, entry.AllowedRoot); err != nil || rebound != entry.CanonicalPath || root != entry.AllowedRoot {
			if err == nil {
				err = fmt.Errorf("managed inventory path binding changed")
			}
			return Inventory{}, ManagedRepository{}, RevocationProgress{}, err
		}
		hasBaseline := false
		for _, baseline := range entry.Baselines {
			if baseline.Actor.ID == actorID && actorID != 0 {
				hasBaseline = true
				break
			}
		}
		if !hasBaseline {
			return Inventory{}, ManagedRepository{}, RevocationProgress{}, fmt.Errorf("quarantine requires a positive baseline for actor %d", actorID)
		}
		for _, progress := range entry.Revocations {
			if progress.Actor.ID == actorID && progress.LastState == StateDenied && progress.ConfirmedAt != "" {
				return inventory, entry, progress, nil
			}
		}
		return Inventory{}, ManagedRepository{}, RevocationProgress{}, fmt.Errorf("quarantine requires persisted confirmed revocation")
	}
	return Inventory{}, ManagedRepository{}, RevocationProgress{}, fmt.Errorf("managed path is not present in inventory: %s", canonical)
}

func discoverQuarantineSources(entry ManagedRepository) ([]string, error) {
	paths := map[string]bool{entry.CanonicalPath: true}
	out, err := gitBytes(entry.CanonicalPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	for _, field := range bytes.Split(out, []byte{0}) {
		value := string(field)
		if !strings.HasPrefix(value, "worktree ") {
			continue
		}
		candidate := strings.TrimPrefix(value, "worktree ")
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("managed Git worktree must be a real directory: %s", candidate)
		}
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return nil, err
		}
		if real == entry.CanonicalPath {
			continue
		}
		if !strictlyWithin(entry.AllowedRoot, real) {
			// A worktree outside the organization-owned root is not derived
			// material this inventory is authorized to move.
			continue
		}
		paths[real] = true
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		if path != entry.CanonicalPath {
			result = append(result, path)
		}
	}
	sort.Strings(result)
	// Move linked worktrees first and the common checkout last.
	result = append(result, entry.CanonicalPath)
	return result, nil
}

func advanceQuarantineMoves(journalPath string, journal QuarantineJournal, failpoint func(string) error, rename func(string, string) error) (QuarantineJournal, error) {
	if rename == nil {
		rename = os.Rename
	}
	journal.Phase = "moving"
	if err := saveQuarantineJournal(journalPath, journal); err != nil {
		return journal, err
	}
	for i := range journal.Sources {
		source := &journal.Sources[i]
		if source.State == "verified" {
			continue
		}
		if source.State != "prepared" && source.State != "moved" {
			return journal, fmt.Errorf("quarantine source %s is not prepared for movement", source.OriginalPath)
		}
		sourceExists := pathExistsNoFollow(source.OriginalPath)
		destinationExists := pathExistsNoFollow(source.QuarantinedPath)
		if source.State == "prepared" {
			switch {
			case sourceExists && !destinationExists:
				if err := rename(source.OriginalPath, source.QuarantinedPath); err != nil {
					return journal, fmt.Errorf("atomic quarantine rename %s: %w", source.OriginalPath, err)
				}
				if err := runQuarantineFailpoint(failpoint, "after-rename-before-journal:"+strconv.Itoa(i)); err != nil {
					return journal, err
				}
				sourceExists, destinationExists = false, true
			case !sourceExists && destinationExists:
				// Crash after rename and before journal persistence.
			case sourceExists && destinationExists:
				return journal, fmt.Errorf("both active and quarantined paths exist for %s", source.OriginalPath)
			default:
				return journal, fmt.Errorf("both active and quarantined paths are missing for %s", source.OriginalPath)
			}
			source.State = "moved"
			if err := saveQuarantineJournal(journalPath, journal); err != nil {
				return journal, err
			}
		}
		if source.State == "moved" {
			if pathExistsNoFollow(source.OriginalPath) || !pathExistsNoFollow(source.QuarantinedPath) {
				return journal, fmt.Errorf("quarantine move state is inconsistent for %s", source.OriginalPath)
			}
			if err := VerifyQuarantinedCheckout(source.CapsulePath, source.QuarantinedPath); err != nil {
				return journal, fmt.Errorf("verify quarantined checkout %s: %w", source.QuarantinedPath, err)
			}
			source.State = "verified"
			if err := saveQuarantineJournal(journalPath, journal); err != nil {
				return journal, err
			}
		}
	}
	journal.Phase = "moved"
	if err := saveQuarantineJournal(journalPath, journal); err != nil {
		return journal, err
	}
	if err := runQuarantineFailpoint(failpoint, "after-moves"); err != nil {
		return journal, err
	}
	return journal, nil
}

func finalizeQuarantineInventory(home string, inventory Inventory, journalPath string, journal QuarantineJournal) error {
	for _, record := range inventory.Quarantines {
		if record.JournalPath == journalPath {
			return nil
		}
	}
	index := -1
	for i := range inventory.Repositories {
		if inventory.Repositories[i].CanonicalPath == journal.InventoryPath {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("cannot finalize quarantine: managed inventory entry is missing")
	}
	purgeEligible := true
	retentionSet := map[string]bool{}
	for _, source := range journal.Sources {
		purgeEligible = purgeEligible && source.PurgeEligible
		for _, reason := range source.RetentionReasons {
			retentionSet[reason] = true
		}
	}
	retention := make([]string, 0, len(retentionSet))
	for reason := range retentionSet {
		retention = append(retention, reason)
	}
	sort.Strings(retention)
	inventory.Quarantines = append(inventory.Quarantines, QuarantineRecord{
		JournalPath: journalPath, Repository: journal.Repository, Actor: journal.Actor,
		ReasonCode: journal.ReasonCode, QuarantinedAt: journal.CreatedAt,
		Sources: append([]QuarantineSource(nil), journal.Sources...), PurgeEligible: purgeEligible,
		RetentionReasons: retention,
	})
	inventory.Repositories = append(inventory.Repositories[:index], inventory.Repositories[index+1:]...)
	sortInventory(&inventory)
	return saveInventory(home, inventory)
}

func saveQuarantineJournal(path string, journal QuarantineJournal) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".journal-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return os.Chmod(path, 0o600)
}

func loadQuarantineJournal(path string) (QuarantineJournal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return QuarantineJournal{}, err
	}
	var journal QuarantineJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return QuarantineJournal{}, err
	}
	if journal.SchemaVersion != quarantineJournalSchemaVersion || journal.InventoryPath == "" || len(journal.Sources) == 0 {
		return QuarantineJournal{}, fmt.Errorf("invalid quarantine journal")
	}
	return journal, nil
}

func allowedRootForJournal(inventory Inventory, journalPath string, journal QuarantineJournal) (string, error) {
	journalAbs, err := filepath.Abs(journalPath)
	if err != nil {
		return "", err
	}
	for _, entry := range inventory.Repositories {
		if entry.CanonicalPath != journal.InventoryPath {
			continue
		}
		if entry.Repository.NodeID != journal.Repository.NodeID || journal.Repository.NodeID == "" {
			return "", fmt.Errorf("quarantine journal repository identity does not match inventory")
		}
		confirmed := false
		for _, progress := range entry.Revocations {
			if progress.Actor.ID == journal.Actor.ID && progress.Actor.NodeID == journal.Actor.NodeID && progress.LastState == StateDenied && progress.ConfirmedAt != "" {
				confirmed = true
				break
			}
		}
		if !confirmed {
			return "", fmt.Errorf("quarantine journal no longer has confirmed revocation evidence")
		}
		return entry.AllowedRoot, nil
	}
	for _, record := range inventory.Quarantines {
		recordPath, err := filepath.Abs(record.JournalPath)
		if err != nil {
			return "", err
		}
		if recordPath != journalAbs {
			continue
		}
		if record.Repository.NodeID != journal.Repository.NodeID || record.Actor.ID != journal.Actor.ID {
			return "", fmt.Errorf("quarantine journal identity does not match retained record")
		}
		return commonAllowedRootFromJournal(journalAbs), nil
	}
	return "", fmt.Errorf("quarantine journal is not bound to active or retained inventory")
}

func validateQuarantineJournal(journalPath string, journal QuarantineJournal, allowedRoot string) error {
	rootReal, err := filepath.EvalSymlinks(allowedRoot)
	if err != nil {
		return err
	}
	if rootReal != allowedRoot {
		return fmt.Errorf("quarantine allowed root is not canonical")
	}
	journalAbs, err := filepath.Abs(journalPath)
	if err != nil {
		return err
	}
	journalInfo, err := os.Lstat(journalAbs)
	if err != nil {
		return err
	}
	if journalInfo.Mode()&os.ModeSymlink != 0 || !journalInfo.Mode().IsRegular() {
		return fmt.Errorf("quarantine journal must be a real file")
	}
	transaction := filepath.Dir(journalAbs)
	transactionReal, err := filepath.EvalSymlinks(transaction)
	if err != nil {
		return err
	}
	if transactionReal != transaction {
		return fmt.Errorf("quarantine transaction path is not canonical")
	}
	quarantineRoot := filepath.Join(rootReal, ".my-cli", "quarantine")
	if !strictlyWithin(quarantineRoot, transactionReal) || filepath.Dir(transactionReal) != quarantineRoot {
		return fmt.Errorf("quarantine transaction is outside the managed quarantine root")
	}
	if journal.InventoryPath == "" || !strictlyWithin(rootReal, journal.InventoryPath) {
		return fmt.Errorf("quarantine inventory path is outside allowed root")
	}
	seenOriginal := map[string]bool{}
	seenDestination := map[string]bool{}
	hasInventorySource := false
	for _, source := range journal.Sources {
		if source.OriginalPath == journal.InventoryPath {
			hasInventorySource = true
		}
		if !strictlyWithin(rootReal, source.OriginalPath) {
			return fmt.Errorf("quarantine source is outside allowed root: %s", source.OriginalPath)
		}
		if !strictlyWithin(transactionReal, source.QuarantinedPath) || filepath.Dir(source.QuarantinedPath) != transactionReal {
			return fmt.Errorf("quarantine destination is outside transaction: %s", source.QuarantinedPath)
		}
		if !strictlyWithin(transactionReal, source.CapsulePath) {
			return fmt.Errorf("recovery capsule is outside transaction: %s", source.CapsulePath)
		}
		if seenOriginal[source.OriginalPath] || seenDestination[source.QuarantinedPath] {
			return fmt.Errorf("duplicate quarantine source or destination")
		}
		seenOriginal[source.OriginalPath] = true
		seenDestination[source.QuarantinedPath] = true
		switch source.State {
		case "preparing", "prepared", "moved", "verified":
		default:
			return fmt.Errorf("invalid quarantine source state %q", source.State)
		}
		if info, err := os.Lstat(source.OriginalPath); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("quarantine source must be a real directory: %s", source.OriginalPath)
			}
			real, err := filepath.EvalSymlinks(source.OriginalPath)
			if err != nil || real != source.OriginalPath {
				return fmt.Errorf("quarantine source path is not canonical: %s", source.OriginalPath)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if info, err := os.Lstat(source.QuarantinedPath); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("quarantine destination must be a real directory: %s", source.QuarantinedPath)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if !hasInventorySource {
		return fmt.Errorf("quarantine journal omits managed inventory source")
	}
	return nil
}

func acquireQuarantineLock(allowedRoot string) (string, func(), error) {
	if strings.TrimSpace(allowedRoot) == "" {
		return "", func() {}, fmt.Errorf("quarantine lock requires allowed root")
	}
	lockDir := filepath.Join(allowedRoot, ".my-cli")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return "", func() {}, err
	}
	lockPath := filepath.Join(lockDir, "access-quarantine.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", func() {}, err
	}
	if err := tryQuarantineFileLock(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errQuarantineLockHeld) {
			return "", func() {}, fmt.Errorf("another quarantine operation is active for %s", allowedRoot)
		}
		return "", func() {}, err
	}
	if err := f.Truncate(0); err != nil {
		_ = unlockQuarantineFile(f)
		_ = f.Close()
		return "", func() {}, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = unlockQuarantineFile(f)
		_ = f.Close()
		return "", func() {}, err
	}
	_, writeErr := fmt.Fprintf(f, "pid=%d\ncreated_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	if writeErr != nil {
		_ = unlockQuarantineFile(f)
		_ = f.Close()
		return "", func() {}, writeErr
	}
	if err := f.Sync(); err != nil {
		_ = unlockQuarantineFile(f)
		_ = f.Close()
		return "", func() {}, err
	}
	release := func() {
		_ = unlockQuarantineFile(f)
		_ = f.Close()
	}
	return lockPath, release, nil
}

func commonAllowedRootFromJournal(journalPath string) string {
	transaction := filepath.Dir(journalPath)
	quarantine := filepath.Dir(transaction)
	myCLI := filepath.Dir(quarantine)
	return filepath.Dir(myCLI)
}

func safeQuarantineName(value string) string {
	var result strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			result.WriteRune(r)
		}
	}
	if result.Len() == 0 {
		return "repository"
	}
	return result.String()
}

func pathExistsNoFollow(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func runQuarantineFailpoint(failpoint func(string) error, phase string) error {
	if failpoint == nil {
		return nil
	}
	if err := failpoint(phase); err != nil {
		return fmt.Errorf("quarantine failpoint %s: %w", phase, err)
	}
	return nil
}

type quarantineSecurityEvent struct {
	EventID        string   `json:"event_id"`
	Type           string   `json:"type"`
	OccurredAt     string   `json:"occurred_at"`
	RepositoryID   int64    `json:"repository_id"`
	RepositoryNode string   `json:"repository_node_id"`
	ActorID        int64    `json:"actor_id"`
	ActorNodeID    string   `json:"actor_node_id"`
	ReasonCode     string   `json:"reason_code"`
	RemovedPaths   []string `json:"removed_paths"`
}

func appendQuarantineSecurityEvent(home, journalPath string, journal QuarantineJournal) error {
	homeDir, err := resolveHome(home)
	if err != nil {
		return err
	}
	eventPath := filepath.Join(homeDir, ".local", "state", "my-cli", "access", "events.jsonl")
	if data, err := os.ReadFile(eventPath); err == nil {
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			var existing quarantineSecurityEvent
			if json.Unmarshal(line, &existing) == nil && existing.EventID == journalPath {
				return nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	paths := make([]string, 0, len(journal.Sources))
	for _, source := range journal.Sources {
		paths = append(paths, source.OriginalPath)
	}
	sort.Strings(paths)
	event := quarantineSecurityEvent{
		EventID: journalPath, Type: "repository-quarantined", OccurredAt: journal.CreatedAt,
		RepositoryID: journal.Repository.ID, RepositoryNode: journal.Repository.NodeID,
		ActorID: journal.Actor.ID, ActorNodeID: journal.Actor.NodeID,
		ReasonCode: journal.ReasonCode, RemovedPaths: paths,
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if err := os.MkdirAll(filepath.Dir(eventPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(eventPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

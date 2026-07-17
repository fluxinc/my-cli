package access

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/safefs"
)

const recoveryCapsuleSchemaVersion = 1

type RecoveryCapsule struct {
	SchemaVersion    int               `json:"schema_version"`
	CreatedAt        string            `json:"created_at"`
	Repository       Repository        `json:"repository"`
	SourcePath       string            `json:"source_path"`
	Head             string            `json:"head"`
	Branch           string            `json:"branch,omitempty"`
	Upstream         string            `json:"upstream,omitempty"`
	Artifacts        []CapsuleArtifact `json:"artifacts"`
	Inventory        []RecoveredFile   `json:"working_tree_inventory"`
	PurgeEligible    bool              `json:"purge_eligible"`
	RetentionReasons []string          `json:"retention_reasons,omitempty"`
}

type CapsuleArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type RecoveredFile struct {
	Path       string      `json:"path"`
	Mode       fs.FileMode `json:"mode"`
	SHA256     string      `json:"sha256,omitempty"`
	LinkTarget string      `json:"link_target,omitempty"`
}

// BuildRecoveryCapsule captures every local recovery surface and verifies a
// byte-for-byte working-tree round trip before returning. The source checkout
// is never moved or modified.
func BuildRecoveryCapsule(source, destination string, repository Repository, now time.Time) (RecoveryCapsule, error) {
	sourceReal, destinationAbs, err := validateCapsulePaths(source, destination)
	if err != nil {
		return RecoveryCapsule{}, err
	}
	if err := os.MkdirAll(destinationAbs, 0o700); err != nil {
		return RecoveryCapsule{}, err
	}
	if err := os.Chmod(destinationAbs, 0o700); err != nil {
		return RecoveryCapsule{}, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.WriteFile(filepath.Join(destinationAbs, "INCOMPLETE"), []byte("Recovery capsule creation did not complete. Do not use this capsule for restoration.\n"), 0o600)
		}
	}()

	head, err := gitText(sourceReal, "rev-parse", "HEAD")
	if err != nil {
		return RecoveryCapsule{}, err
	}
	branch, _ := gitText(sourceReal, "symbolic-ref", "--quiet", "--short", "HEAD")
	upstream, _ := gitText(sourceReal, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")

	patchPath := filepath.Join(destinationAbs, "working-tree.patch")
	patch, err := gitBytes(sourceReal, "diff", "--binary", "--full-index", "HEAD", "--")
	if err != nil {
		return RecoveryCapsule{}, err
	}
	if err := os.WriteFile(patchPath, patch, 0o600); err != nil {
		return RecoveryCapsule{}, err
	}
	indexPatch, err := gitBytes(sourceReal, "diff", "--cached", "--binary", "--full-index", "HEAD", "--")
	if err != nil {
		return RecoveryCapsule{}, err
	}
	if err := os.WriteFile(filepath.Join(destinationAbs, "index.patch"), indexPatch, 0o600); err != nil {
		return RecoveryCapsule{}, err
	}
	unstagedPatch, err := gitBytes(sourceReal, "diff", "--binary", "--full-index", "--")
	if err != nil {
		return RecoveryCapsule{}, err
	}
	if err := os.WriteFile(filepath.Join(destinationAbs, "unstaged.patch"), unstagedPatch, 0o600); err != nil {
		return RecoveryCapsule{}, err
	}
	bundlePath := filepath.Join(destinationAbs, "local-refs.bundle")
	if _, err := gitBytes(sourceReal, "bundle", "create", bundlePath, "--all"); err != nil {
		return RecoveryCapsule{}, err
	}
	if err := os.Chmod(bundlePath, 0o600); err != nil {
		return RecoveryCapsule{}, err
	}
	untrackedPath := filepath.Join(destinationAbs, "untracked-and-ignored.tar")
	if err := writeUntrackedArchive(sourceReal, untrackedPath); err != nil {
		return RecoveryCapsule{}, err
	}

	inventory, err := workingTreeInventory(sourceReal)
	if err != nil {
		return RecoveryCapsule{}, err
	}
	purgeEligible, reasons, err := purgeVerdict(sourceReal)
	if err != nil {
		return RecoveryCapsule{}, err
	}
	createdAt := now.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	capsule := RecoveryCapsule{
		SchemaVersion:    recoveryCapsuleSchemaVersion,
		CreatedAt:        createdAt.Format(time.RFC3339Nano),
		Repository:       repository,
		SourcePath:       sourceReal,
		Head:             head,
		Branch:           branch,
		Upstream:         upstream,
		Inventory:        inventory,
		PurgeEligible:    purgeEligible,
		RetentionReasons: reasons,
	}
	for _, name := range recoveryArtifactNames() {
		artifact, err := capsuleArtifact(filepath.Join(destinationAbs, name), name)
		if err != nil {
			return RecoveryCapsule{}, err
		}
		capsule.Artifacts = append(capsule.Artifacts, artifact)
	}
	manifestData, err := json.MarshalIndent(capsule, "", "  ")
	if err != nil {
		return RecoveryCapsule{}, err
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(destinationAbs, "recovery.json"), manifestData, 0o600); err != nil {
		return RecoveryCapsule{}, err
	}
	if err := VerifyRecoveryCapsule(destinationAbs); err != nil {
		return RecoveryCapsule{}, fmt.Errorf("verify recovery capsule: %w", err)
	}
	succeeded = true
	return capsule, nil
}

// VerifyRecoveryCapsule verifies artifact hashes and reconstructs the working
// tree from HEAD + patch + untracked archive, then byte-compares it with the
// recorded inventory.
func VerifyRecoveryCapsule(capsuleDir string) error {
	capsule, err := loadRecoveryCapsule(capsuleDir)
	if err != nil {
		return err
	}
	if err := validateRecoveryArtifacts(capsule.Artifacts); err != nil {
		return err
	}
	for _, artifact := range capsule.Artifacts {
		actual, err := capsuleArtifact(filepath.Join(capsuleDir, artifact.Name), artifact.Name)
		if err != nil {
			return err
		}
		if actual.SHA256 != artifact.SHA256 || actual.Size != artifact.Size {
			return fmt.Errorf("artifact %s checksum mismatch", artifact.Name)
		}
	}
	verifyRoot, err := os.MkdirTemp(filepath.Dir(capsuleDir), ".recovery-verify-")
	if err != nil {
		return err
	}
	defer func() { _ = safefs.RemoveAll(verifyRoot) }()
	bundlePath := filepath.Join(capsuleDir, "local-refs.bundle")
	bare := filepath.Join(verifyRoot, "verification.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); err != nil {
		return fmt.Errorf("initialize bundle verification repository: %s", commandMessage(out, err))
	}
	if _, err := gitBytes(bare, "bundle", "verify", bundlePath); err != nil {
		return fmt.Errorf("verify Git bundle: %w", err)
	}
	restored := filepath.Join(verifyRoot, "repository")
	if out, err := exec.Command("git", "clone", "--no-checkout", bundlePath, restored).CombinedOutput(); err != nil {
		return fmt.Errorf("clone recovery bundle: %s", commandMessage(out, err))
	}
	if _, err := gitBytes(restored, "checkout", "--detach", capsule.Head); err != nil {
		return err
	}
	indexPatch := filepath.Join(capsuleDir, "index.patch")
	if info, err := os.Stat(indexPatch); err != nil {
		return err
	} else if info.Size() != 0 {
		if _, err := gitBytes(restored, "apply", "--binary", "--index", indexPatch); err != nil {
			return fmt.Errorf("apply staged recovery patch: %w", err)
		}
	}
	unstagedPatch := filepath.Join(capsuleDir, "unstaged.patch")
	if info, err := os.Stat(unstagedPatch); err != nil {
		return err
	} else if info.Size() != 0 {
		if _, err := gitBytes(restored, "apply", "--binary", unstagedPatch); err != nil {
			return fmt.Errorf("apply unstaged recovery patch: %w", err)
		}
	}
	if err := extractUntrackedArchive(filepath.Join(capsuleDir, "untracked-and-ignored.tar"), restored); err != nil {
		return err
	}
	if err := applyRecordedFileModes(restored, capsule.Inventory); err != nil {
		return err
	}
	actual, err := workingTreeInventory(restored)
	if err != nil {
		return err
	}
	if !equalRecoveredFiles(capsule.Inventory, actual) {
		return fmt.Errorf("restored working tree does not match recorded byte inventory")
	}
	return nil
}

func loadRecoveryCapsule(dir string) (RecoveryCapsule, error) {
	if _, err := os.Stat(filepath.Join(dir, "INCOMPLETE")); err == nil {
		return RecoveryCapsule{}, fmt.Errorf("recovery capsule is marked incomplete")
	} else if !errors.Is(err, os.ErrNotExist) {
		return RecoveryCapsule{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "recovery.json"))
	if err != nil {
		return RecoveryCapsule{}, err
	}
	var capsule RecoveryCapsule
	if err := json.Unmarshal(data, &capsule); err != nil {
		return RecoveryCapsule{}, err
	}
	if capsule.SchemaVersion != recoveryCapsuleSchemaVersion || capsule.SourcePath == "" || capsule.Head == "" {
		return RecoveryCapsule{}, fmt.Errorf("invalid recovery capsule manifest")
	}
	return capsule, nil
}

func recoveryArtifactNames() []string {
	return []string{
		"working-tree.patch",
		"index.patch",
		"unstaged.patch",
		"local-refs.bundle",
		"untracked-and-ignored.tar",
	}
}

func validateRecoveryArtifacts(artifacts []CapsuleArtifact) error {
	expected := recoveryArtifactNames()
	if len(artifacts) != len(expected) {
		return fmt.Errorf("invalid recovery capsule artifact set")
	}
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		if seen[artifact.Name] {
			return fmt.Errorf("duplicate recovery artifact %q", artifact.Name)
		}
		seen[artifact.Name] = true
	}
	for _, name := range expected {
		if !seen[name] {
			return fmt.Errorf("missing recovery artifact %q", name)
		}
	}
	return nil
}

func validateCapsulePaths(source, destination string) (string, string, error) {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(destination) == "" {
		return "", "", fmt.Errorf("source and capsule destination are required")
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(sourceAbs)
	if err != nil {
		return "", "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", fmt.Errorf("recovery source must be a real directory")
	}
	sourceReal, err := filepath.EvalSymlinks(sourceAbs)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(sourceReal, ".git")); err != nil {
		return "", "", fmt.Errorf("recovery source is not a Git checkout: %w", err)
	}
	destinationAbs, err := filepath.Abs(destination)
	if err != nil {
		return "", "", err
	}
	destinationAbs, err = canonicalProspectivePath(destinationAbs)
	if err != nil {
		return "", "", err
	}
	if withinOrEqualPath(sourceReal, destinationAbs) {
		return "", "", fmt.Errorf("recovery capsule must be outside the source checkout")
	}
	if _, err := os.Lstat(destinationAbs); err == nil {
		return "", "", fmt.Errorf("recovery capsule destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	return sourceReal, destinationAbs, nil
}

func writeUntrackedArchive(repo, target string) error {
	paths := map[string]bool{}
	for _, args := range [][]string{
		{"ls-files", "-z", "--others", "--exclude-standard"},
		{"ls-files", "-z", "--others", "--ignored", "--exclude-standard"},
	} {
		out, err := gitBytes(repo, args...)
		if err != nil {
			return err
		}
		for _, item := range strings.Split(string(out), "\x00") {
			if item == "" {
				continue
			}
			if strings.HasSuffix(item, "/") {
				if err := collectArchiveTree(repo, strings.TrimSuffix(item, "/"), paths); err != nil {
					return err
				}
				continue
			}
			paths[item] = true
		}
	}
	if err := collectEmbeddedRepositoryTrees(repo, paths); err != nil {
		return err
	}
	if err := collectWorkingTreeDirectories(repo, paths); err != nil {
		return err
	}
	ordered := make([]string, 0, len(paths))
	for item := range paths {
		ordered = append(ordered, item)
	}
	sort.Strings(ordered)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	closeWithError := func(current error) error {
		if err := tw.Close(); current == nil {
			current = err
		}
		if err := f.Sync(); current == nil {
			current = err
		}
		if err := f.Close(); current == nil {
			current = err
		}
		return current
	}
	for _, rel := range ordered {
		if !portableArchivePath(rel) {
			return closeWithError(fmt.Errorf("unsafe untracked path %q", rel))
		}
		full := filepath.Join(repo, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			return closeWithError(err)
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(full)
			if err != nil {
				return closeWithError(err)
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return closeWithError(err)
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return closeWithError(err)
		}
		if info.Mode().IsRegular() {
			in, err := os.Open(full)
			if err != nil {
				return closeWithError(err)
			}
			_, copyErr := io.Copy(tw, in)
			closeErr := in.Close()
			if copyErr != nil {
				return closeWithError(copyErr)
			}
			if closeErr != nil {
				return closeWithError(closeErr)
			}
		} else if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
			return closeWithError(fmt.Errorf("unsupported untracked file type %s", rel))
		}
	}
	return closeWithError(nil)
}

func extractUntrackedArchive(archivePath, destination string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	type directoryMode struct {
		path string
		mode fs.FileMode
	}
	var directories []directoryMode
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			for i := len(directories) - 1; i >= 0; i-- {
				if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		if !portableArchivePath(header.Name) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		if !withinOrEqualPath(destination, target) {
			return fmt.Errorf("archive path escapes destination: %s", header.Name)
		}
		if err := rejectSymlinkParents(destination, target); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		existing, statErr := os.Lstat(target)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if statErr == nil {
				return fmt.Errorf("archive file target already exists: %s", header.Name)
			}
			mode := fs.FileMode(header.Mode) & fs.ModePerm
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if statErr == nil {
				return fmt.Errorf("archive symlink target already exists: %s", header.Name)
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeDir:
			if statErr == nil && (!existing.IsDir() || existing.Mode()&os.ModeSymlink != 0) {
				return fmt.Errorf("archive directory target is not a real directory: %s", header.Name)
			}
			mode := fs.FileMode(header.Mode) & fs.ModePerm
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			directories = append(directories, directoryMode{path: target, mode: mode})
		default:
			return fmt.Errorf("unsupported archive entry type for %s", header.Name)
		}
	}
}

func collectArchiveTree(repo, relativeRoot string, paths map[string]bool) error {
	if !portableArchivePath(relativeRoot) {
		return fmt.Errorf("unsafe untracked path %q", relativeRoot)
	}
	root := filepath.Join(repo, filepath.FromSlash(relativeRoot))
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !portableArchivePath(rel) {
			return fmt.Errorf("unsafe embedded repository path %q", rel)
		}
		paths[rel] = true
		return nil
	})
}

func collectEmbeddedRepositoryTrees(repo string, paths map[string]bool) error {
	return filepath.WalkDir(repo, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		if rel == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != ".git" {
			return nil
		}
		embeddedRoot, err := filepath.Rel(repo, filepath.Dir(path))
		if err != nil {
			return err
		}
		if err := collectArchiveTree(repo, filepath.ToSlash(embeddedRoot), paths); err != nil {
			return err
		}
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}

func collectWorkingTreeDirectories(repo string, paths map[string]bool) error {
	return filepath.WalkDir(repo, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			rel = filepath.ToSlash(rel)
			if !portableArchivePath(rel) {
				return fmt.Errorf("unsafe working-tree directory %q", rel)
			}
			paths[rel] = true
		}
		return nil
	})
}

func applyRecordedFileModes(root string, inventory []RecoveredFile) error {
	var directories []RecoveredFile
	for _, item := range inventory {
		if !portableArchivePath(item.Path) {
			return fmt.Errorf("unsafe inventory path %q", item.Path)
		}
		target := filepath.Join(root, filepath.FromSlash(item.Path))
		if !withinOrEqualPath(root, target) {
			return fmt.Errorf("inventory path escapes restored checkout: %s", item.Path)
		}
		if err := rejectSymlinkParents(root, target); err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if item.Mode.IsDir() {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("recorded directory is not a real directory: %s", item.Path)
			}
			directories = append(directories, item)
			continue
		}
		if !item.Mode.IsRegular() {
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("recorded file is not a regular file: %s", item.Path)
		}
		if err := os.Chmod(target, item.Mode.Perm()); err != nil {
			return fmt.Errorf("restore mode for %s: %w", item.Path, err)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		leftDepth := strings.Count(directories[i].Path, "/")
		rightDepth := strings.Count(directories[j].Path, "/")
		if leftDepth == rightDepth {
			return directories[i].Path < directories[j].Path
		}
		return leftDepth > rightDepth
	})
	for _, item := range directories {
		target := filepath.Join(root, filepath.FromSlash(item.Path))
		if err := os.Chmod(target, item.Mode.Perm()); err != nil {
			return fmt.Errorf("restore directory mode for %s: %w", item.Path, err)
		}
	}
	return nil
}

func purgeVerdict(repo string) (bool, []string, error) {
	// A local checkout cannot authoritatively prove that its remote retains all
	// data required by an organization's retention policy. Keep every capsule
	// until a separate, governed QMS decision records that proof.
	reasons := []string{"remote-retention-not-authoritatively-proven"}
	status, err := gitBytes(repo, "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "-z")
	if err != nil {
		return false, nil, err
	}
	if len(status) != 0 {
		reasons = append(reasons, "working-tree-content")
	}
	localCommits, err := gitText(repo, "rev-list", "--all", "--not", "--remotes")
	if err != nil {
		return false, nil, err
	}
	if localCommits != "" {
		reasons = append(reasons, "local-only-commits-or-refs")
	}
	stashes, err := gitText(repo, "stash", "list", "--format=%H")
	if err != nil {
		return false, nil, err
	}
	if stashes != "" {
		reasons = append(reasons, "stashes")
	}
	tags, err := gitText(repo, "for-each-ref", "--format=%(refname)", "refs/tags")
	if err != nil {
		return false, nil, err
	}
	if tags != "" {
		reasons = append(reasons, "tags-not-proven-remote")
	}
	sort.Strings(reasons)
	return len(reasons) == 0, reasons, nil
}

func workingTreeInventory(root string) ([]RecoveredFile, error) {
	var files []RecoveredFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		item := RecoveredFile{Path: filepath.ToSlash(rel), Mode: info.Mode()}
		switch {
		case info.IsDir():
		case info.Mode()&os.ModeSymlink != 0:
			item.LinkTarget, err = os.Readlink(path)
		case info.Mode().IsRegular():
			item.SHA256, _, err = fileSHA256(path)
		default:
			err = fmt.Errorf("unsupported working-tree file type %s", rel)
		}
		if err != nil {
			return err
		}
		files = append(files, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func capsuleArtifact(path, name string) (CapsuleArtifact, error) {
	hash, size, err := fileSHA256(path)
	if err != nil {
		return CapsuleArtifact{}, err
	}
	return CapsuleArtifact{Name: name, SHA256: hash, Size: size}, nil
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), size, nil
}

func equalRecoveredFiles(left, right []RecoveredFile) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func portableArchivePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func withinOrEqualPath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func rejectSymlinkParents(root, target string) error {
	rel, err := filepath.Rel(root, filepath.Dir(target))
	if err != nil {
		return err
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive path traverses symlink: %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("archive parent is not a directory: %s", current)
		}
	}
	return nil
}

// canonicalProspectivePath resolves symlinks in the deepest existing ancestor
// without requiring the prospective path itself to exist.
func canonicalProspectivePath(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func gitText(repo string, args ...string) (string, error) {
	out, err := gitBytes(repo, args...)
	return strings.TrimSpace(string(out)), err
}

func gitBytes(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), commandMessage(out, err))
	}
	return out, nil
}

func commandMessage(out []byte, err error) string {
	message := strings.TrimSpace(string(out))
	if message != "" {
		return message
	}
	return err.Error()
}

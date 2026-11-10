package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fluxinc/my-cli/internal/launchplan"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

// Reasons a mount carries no pin. They are codes, never Git's own text: a
// diagnostic must not print a configured remote, which can hold a credential.
const (
	pinMissingCheckout    = "missing_checkout"
	pinNoUpstream         = "no_upstream"
	pinRepositoryMismatch = "repository_mismatch"
	pinAheadOfUpstream    = "ahead_of_upstream"
	pinError              = "error"
	pinSourceUmbrella     = "umbrella"
)

var fullCommitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// loadCompileUmbrella resolves dir to a canonical umbrella root and reads its
// identity. dir must itself be the root: it is never found by walking up, so
// a session directory or any other path inside an umbrella is refused rather
// than relabelled.
func loadCompileUmbrella(dir string) (string, umbrella.Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", umbrella.Workspace{}, err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", umbrella.Workspace{}, fmt.Errorf("--umbrella %s: %w", dir, err)
	}
	ws, err := umbrella.LoadWorkspace(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", umbrella.Workspace{}, fmt.Errorf("--umbrella %s is not an umbrella root: it has no .my-cli/%s; pass the umbrella's own directory", dir, umbrella.WorkspaceFile)
	}
	if err != nil {
		return "", umbrella.Workspace{}, err
	}
	if ws.SchemaVersion < 1 || ws.SchemaVersion > umbrella.SchemaVersion {
		return "", umbrella.Workspace{}, fmt.Errorf("--umbrella %s has workspace schema %d; this my reads up to %d", dir, ws.SchemaVersion, umbrella.SchemaVersion)
	}
	if strings.TrimSpace(ws.Organization) == "" {
		return "", umbrella.Workspace{}, fmt.Errorf("--umbrella %s records no organization", dir)
	}
	return root, ws, nil
}

// checkCompileUmbrella requires the umbrella to belong to the manifest being
// compiled, so one organization's checkouts never pin another's projection.
func checkCompileUmbrella(root string, ws umbrella.Workspace, doc manifest.Document) error {
	if ws.Organization != doc.Organization.ID {
		return fmt.Errorf("umbrella %s belongs to organization %q, but the manifest being compiled is for %q", root, ws.Organization, doc.Organization.ID)
	}
	return nil
}

// validateCompileUmbrella is the two checks together.
func validateCompileUmbrella(dir string, doc manifest.Document) (string, error) {
	root, ws, err := loadCompileUmbrella(dir)
	if err != nil {
		return "", err
	}
	return root, checkCompileUmbrella(root, ws, doc)
}

// pinGitRunner runs one git subcommand in dir. It returns standard output and
// the exit status; err is set only when git could not be run at all.
type pinGitRunner func(dir string, args ...string) (stdout []byte, exit int, err error)

// runPinGit is read-only and offline by construction, not by convention:
//   - protocol.allow=never refuses every transport, so a partial clone cannot
//     lazily fetch a missing object on any Git version;
//   - GIT_NO_LAZY_FETCH says the same to Git versions that know it;
//   - optional locks and terminal prompts are off, and no fsmonitor hook runs;
//   - GIT_CEILING_DIRECTORIES stops discovery at the mount's parent, so a
//     plain directory is never answered for by an ancestor repository.
func runPinGit(dir string, args ...string) ([]byte, int, error) {
	full := append([]string{"-C", dir, "-c", "protocol.allow=never", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_NO_LAZY_FETCH=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(dir),
	)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), nil
	}
	if err != nil {
		return nil, -1, err
	}
	return out, 0, nil
}

func observeMountPin(root string, m manifest.Mount) launchplan.MountPin {
	return observeMountPinWith(root, m, runPinGit)
}

// observeMountPinWith reports the commit checked out in a mount's base
// checkout, with the local evidence the Pin type documents. Every probe is
// local Git. Anything it cannot establish is a reason code, never a pin.
func observeMountPinWith(root string, m manifest.Mount, run pinGitRunner) launchplan.MountPin {
	unavailable := func(reason string) launchplan.MountPin {
		return launchplan.MountPin{Pin: launchplan.Pin{Unavailable: reason}}
	}
	// git returns trimmed output, whether the command exited 0, and whether
	// the answer is usable at all.
	git := func(dir string, args ...string) (text string, ok, usable bool) {
		out, exit, err := run(dir, args...)
		if err != nil || exit < 0 {
			return "", false, false
		}
		return strings.TrimSpace(string(out)), exit == 0, true
	}

	// Base mount only. Session worktrees are never read.
	path := umbrella.MountPath(root, m.ID)
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return unavailable(pinMissingCheckout)
	case err != nil:
		return unavailable(pinError)
	case !info.IsDir():
		return unavailable(pinMissingCheckout)
	}
	if _, err := os.Lstat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		// An ordinary directory, whatever repository may contain it.
		return unavailable(pinMissingCheckout)
	} else if err != nil {
		return unavailable(pinError)
	}
	top, ok, usable := git(path, "rev-parse", "--show-toplevel")
	if !usable || !ok {
		return unavailable(pinError)
	}
	if canonicalPath(top) != canonicalPath(path) {
		return unavailable(pinMissingCheckout)
	}

	// Capture HEAD once, as a full commit id. Every later check uses this
	// literal id, so a checkout that moves meanwhile cannot be vouched for.
	head, ok, usable := git(path, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if !usable || !ok || !fullCommitID.MatchString(head) {
		return unavailable(pinError)
	}

	branch, ok, usable := git(path, "symbolic-ref", "-q", "HEAD")
	if !usable {
		return unavailable(pinError)
	}
	if !ok || branch == "" {
		return unavailable(pinNoUpstream) // detached HEAD
	}
	tracking, ok, usable := git(path, "for-each-ref", "--format=%(upstream)%09%(upstream:remotename)", branch)
	if !usable || !ok {
		return unavailable(pinError)
	}
	upstream, remote, _ := strings.Cut(tracking, "\t")
	// A local branch as upstream (remote ".") is not evidence about a remote.
	if upstream == "" || remote == "" || remote == "." || !strings.HasPrefix(upstream, "refs/remotes/") {
		return unavailable(pinNoUpstream)
	}

	// The cached upstream must belong to the repository this mount projects.
	// The comparison is the manifest's own, which is conservative: the same
	// repository over another transport reads as a mismatch.
	url, ok, usable := git(path, "config", "--get", "remote."+remote+".url")
	if !usable {
		return unavailable(pinError)
	}
	if !ok || !manifest.SameRemote(url, m.GitURL) {
		return unavailable(pinRepositoryMismatch)
	}

	upstreamCommit, ok, usable := git(path, "rev-parse", "--verify", "--quiet", upstream+"^{commit}")
	if !usable || !ok || !fullCommitID.MatchString(upstreamCommit) {
		return unavailable(pinError) // includes an object this clone does not have
	}
	out, exit, err := run(path, "merge-base", "--is-ancestor", head, upstreamCommit)
	_ = out
	switch {
	case err != nil || exit < 0:
		return unavailable(pinError)
	case exit == 1:
		return unavailable(pinAheadOfUpstream)
	case exit != 0:
		return unavailable(pinError)
	}

	status, exit, err := run(path, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil || exit != 0 {
		return unavailable(pinError)
	}
	return launchplan.MountPin{
		Pin:   launchplan.Pin{Commit: head, Source: pinSourceUmbrella},
		Dirty: len(status) > 0,
	}
}

// canonicalPath resolves symlinks so /tmp and /private/tmp compare equal.
func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

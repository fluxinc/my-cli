package attempt

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Observation kinds.
const (
	KindBefore      = "before"
	KindAfter       = "after"
	KindAtReconcile = "at_reconcile"
)

// RepoTarget names one Git checkout to observe.
type RepoTarget struct {
	Label string
	Path  string
}

// Observation is what the local checkouts looked like at one moment. It uses
// only local Git state: no fetch and no network. "Ahead" compares against the
// cached upstream ref, so it is evidence about this clone, not proof of what
// the remote holds.
type Observation struct {
	Kind        string    `json:"kind"`
	CollectedAt string    `json:"collected_at"`
	Repos       []RepoObs `json:"repos"`
}

// RepoObs is one checkout's state. Nil fields are unknown, never zero.
type RepoObs struct {
	Label  string `json:"label,omitempty"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	// Dirty counts tracked changes and untracked files.
	Dirty       *bool `json:"dirty,omitempty"`
	HasUpstream *bool `json:"has_upstream,omitempty"`
	// Ahead is nil when there is no upstream: publication is unknown.
	Ahead *int   `json:"ahead,omitempty"`
	Error string `json:"error,omitempty"`
}

// GitRunner runs git in dir and returns its standard output.
type GitRunner func(dir string, args ...string) ([]byte, error)

// RunGit is the default GitRunner. It disables optional locks so observing
// never contends with a harness for the index.
func RunGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd.Output()
}

// Observe collects one observation with a single local git call per repo.
func Observe(kind string, repos []RepoTarget, run GitRunner, now time.Time) *Observation {
	obs := &Observation{Kind: kind, CollectedAt: now.UTC().Format(time.RFC3339), Repos: []RepoObs{}}
	for _, target := range repos {
		repo := RepoObs{Label: target.Label, Path: target.Path}
		// my's own state directory is excluded: recording a launch must not
		// make the umbrella root look changed.
		out, err := run(target.Path, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal", "--", ".", ":(top,exclude).my-cli")
		if err != nil {
			repo.Error = gitError(err)
		} else {
			parseStatus(out, &repo)
		}
		obs.Repos = append(obs.Repos, repo)
	}
	return obs
}

func parseStatus(out []byte, repo *RepoObs) {
	dirty, upstream := false, false
	for _, field := range strings.Split(string(out), "\x00") {
		switch {
		case field == "":
		case strings.HasPrefix(field, "# branch.oid "):
			if oid := strings.TrimPrefix(field, "# branch.oid "); oid != "(initial)" {
				repo.Head = oid
			}
		case strings.HasPrefix(field, "# branch.head "):
			repo.Branch = strings.TrimPrefix(field, "# branch.head ")
		case strings.HasPrefix(field, "# branch.upstream "):
			upstream = true
		case strings.HasPrefix(field, "# branch.ab "):
			parts := strings.Fields(strings.TrimPrefix(field, "# branch.ab "))
			if len(parts) > 0 {
				if n, err := strconv.Atoi(strings.TrimPrefix(parts[0], "+")); err == nil {
					repo.Ahead = &n
				}
			}
		case strings.HasPrefix(field, "# "):
		default:
			// Any entry record (1, 2, u, ?) or a rename's second path.
			dirty = true
		}
	}
	repo.Dirty = &dirty
	repo.HasUpstream = &upstream
	if !upstream {
		repo.Ahead = nil
	}
}

func gitError(err error) string {
	if exit, ok := err.(*exec.ExitError); ok {
		if text := strings.TrimSpace(string(exit.Stderr)); text != "" {
			if len(text) > 300 {
				text = text[:300]
			}
			return text
		}
	}
	return err.Error()
}

// LocalChange derives local_change_observed. It returns true on any positive
// evidence: a checkout that ends dirty or ahead, or whose HEAD, branch, dirty
// state or ahead count differs between the two observations. It returns false
// only when every checkout has complete evidence at both ends and nothing
// differs, and nil otherwise.
func LocalChange(before, after *Observation) *bool {
	yes, no := true, false
	if after == nil || len(after.Repos) == 0 {
		// Nothing was observed, so nothing can be said either way.
		return nil
	}
	earlier := map[string]RepoObs{}
	if before != nil {
		for _, repo := range before.Repos {
			earlier[repo.Path] = repo
		}
	}
	unknown := before == nil
	for _, repo := range after.Repos {
		if repo.Dirty != nil && *repo.Dirty {
			return &yes
		}
		if repo.Ahead != nil && *repo.Ahead > 0 {
			return &yes
		}
		prior, seen := earlier[repo.Path]
		if seen && prior.Error == "" && repo.Error == "" {
			if prior.Head != repo.Head || prior.Branch != repo.Branch {
				return &yes
			}
			if prior.Dirty != nil && repo.Dirty != nil && *prior.Dirty != *repo.Dirty {
				return &yes
			}
			if prior.Ahead != nil && repo.Ahead != nil && *prior.Ahead != *repo.Ahead {
				return &yes
			}
		}
		// "No change" needs complete evidence at both ends for this checkout.
		complete := func(r RepoObs) bool { return r.Error == "" && r.Dirty != nil && r.Ahead != nil }
		if !seen || !complete(prior) || !complete(repo) {
			unknown = true
		}
	}
	if unknown {
		return nil
	}
	return &no
}

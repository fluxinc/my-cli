package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/contractstate"
	"github.com/fluxinc/my-cli/internal/guidance"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/worksession"
)

type contractChange struct {
	From *contractstate.Revision `json:"from,omitempty"`
	To   contractstate.Revision  `json:"to"`
	Diff string                  `json:"diff"`
}

type contractRootResult struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// currentContractSnapshot checks the same checkout bytes before and after
// resolution. Local edits can be inspected but are not treated as published
// revisions of a registered remote manifest.
func currentContractSnapshot(ref manifest.Ref) (contractstate.Snapshot, error) {
	root := ref.LocalPath
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return contractstate.Snapshot{}, err
	}
	head, headErr := gitPRText(root, nil, "rev-parse", "HEAD^{commit}")
	if headErr != nil {
		head = ""
	}
	var doc manifest.Document
	if err = json.Unmarshal(raw, &doc); err != nil {
		return contractstate.Snapshot{}, err
	}
	validation := manifest.ValidateDocument(root, doc)
	if len(validation.Errors) != 0 {
		return contractstate.Snapshot{}, &manifest.InvalidError{Name: ref.Name, Result: validation}
	}
	contract, err := manifest.LoadContract(root, doc)
	if err != nil {
		return contractstate.Snapshot{}, err
	}
	text := string(contract.Bytes())
	if contract.File == "" && text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	snapshot := contractstate.Snapshot{Configured: contract.File != "" || len(contract.Rules()) != 0, Source: contract.Source(), Path: contract.File, Text: text, Provenance: "local-only", Revision: contractstate.Revision{ManifestCommit: head, Blob: manifest.ContractBlob([]byte(text))}}
	if head != "" {
		if _, remoteErr := gitPRText(root, nil, "remote", "get-url", "origin"); remoteErr == nil {
			snapshot.Provenance = "committed"
			paths := []string{"manifest.json"}
			if contract.File != "" {
				paths = append(paths, contract.File)
			}
			args := append([]string{"diff", "--quiet", "HEAD", "--"}, paths...)
			if _, err := gitPRBytes(root, nil, args...); err != nil {
				snapshot.Provenance = "unpublished"
			}
			for _, p := range paths {
				if _, err := gitPRBytes(root, nil, "cat-file", "-e", "HEAD:"+p); err != nil {
					snapshot.Provenance = "unpublished"
				}
			}
			upstream, err := gitPRText(root, nil, "rev-parse", "--verify", "@{upstream}^{commit}")
			if err != nil {
				snapshot.Provenance = "unpublished"
			} else if _, err := gitPRBytes(root, nil, "merge-base", "--is-ancestor", head, upstream); err != nil {
				snapshot.Provenance = "unpublished"
			}
		}
	}
	after, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil || !bytes.Equal(raw, after) {
		return contractstate.Snapshot{}, fmt.Errorf("manifest changed while reading contract; retry")
	}
	if head != "" {
		afterHead, err := gitPRText(root, nil, "rev-parse", "HEAD^{commit}")
		if err != nil || afterHead != head {
			return contractstate.Snapshot{}, fmt.Errorf("manifest commit changed while reading contract; retry")
		}
	}
	if contract.File != "" {
		again, err := manifest.LoadContract(root, doc)
		if err != nil || again.Blob != contract.Blob {
			return contractstate.Snapshot{}, fmt.Errorf("contract file changed while reading; retry")
		}
	}
	return snapshot, nil
}

func contractRoots(root string) ([]contractstate.Root, error) {
	if root == "" {
		return nil, nil
	}
	roots := []contractstate.Root{{Path: root, Kind: "umbrella", Umbrella: root}}
	sessions, err := worksession.List(root)
	if err != nil {
		return roots, err
	}
	for _, session := range sessions {
		if session.Status == worksession.StatusActive {
			roots = append(roots, contractstate.Root{Path: session.Path, Kind: "session", SessionID: session.ID, Umbrella: root})
		}
	}
	return roots, nil
}

// observeContract records a revision before any derived writes. Repeating an
// observation cannot clear pending installation or advance an emission cursor.
func observeContract(home, name, root string, fetched bool) (contractstate.State, *contractChange, error) {
	ref, ok, err := manifest.Find(home, name)
	if err != nil {
		return contractstate.State{}, nil, err
	}
	if !ok {
		return contractstate.State{}, nil, fmt.Errorf("manifest %q not registered", name)
	}
	existing, err := contractstate.Load(home, name)
	if err != nil {
		return existing, nil, err
	}
	if existing.Active == nil {
		doc, _, err := manifest.LoadDocument(ref.LocalPath)
		if err != nil {
			return existing, nil, err
		}
		if doc.ContractFile == "" && len(doc.Contract) == 0 {
			return existing, nil, nil
		}
	}
	snapshot, err := currentContractSnapshot(ref)
	if err != nil {
		return existing, nil, err
	}
	if !snapshot.Configured && existing.Active == nil {
		return existing, nil, nil
	}
	if snapshot.Provenance == "unpublished" {
		return existing, nil, nil
	}
	var change *contractChange
	state, err := contractstate.Update(home, name, func(s *contractstate.State) error {
		current, err := currentContractSnapshot(ref)
		if err != nil {
			return err
		}
		if current.Provenance == "unpublished" {
			return nil
		}
		roots, rootsErr := knownContractRoots(name, root, *s)
		prior := s.Active
		if s.Observe(current) && prior != nil && prior.Text != current.Text {
			rev := prior.Revision
			change = &contractChange{From: &rev, To: current.Revision, Diff: contractLineDiff(prior.Text, current.Text)}
		}
		// Rebuild the live root inventory only after a complete enumeration.
		if rootsErr == nil {
			live := map[string]bool{}
			for _, r := range roots {
				live[r.Path] = true
			}
			for path := range s.Roots {
				if !live[path] {
					delete(s.Roots, path)
				}
			}
		}
		for _, r := range roots {
			s.Register(r.Path, r.Kind, r.SessionID)
			stored := s.Roots[r.Path]
			stored.Umbrella = r.Umbrella
			s.Roots[r.Path] = stored
		}
		if fetched {
			s.LastSuccessfulRemoteCheck = time.Now().UTC().Format(time.RFC3339)
		}
		return nil
	})
	if err != nil {
		return state, nil, err
	}
	// Inventory errors are per-root reconciliation failures; the active
	// revision remains durable and healthy roots can still be updated.
	return state, change, nil
}

// contractLineDiff gives a bounded-memory line diff. Unique contract lines
// anchor unchanged spans; moved lines are shown as removal and addition.
func contractLineDiff(old, new string) string {
	if old == new {
		return ""
	}
	lines := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	}
	a, b := lines(old), lines(new)
	// A linear-space lookahead avoids quadratic memory for large contracts.
	positions := map[string][]int{}
	for j, line := range b {
		positions[line] = append(positions[line], j)
	}
	var out strings.Builder
	out.WriteString("--- previous contract\n+++ active contract\n")
	i, j := 0, 0
	for i < len(a) {
		if j < len(b) && a[i] == b[j] {
			out.WriteString(" " + a[i] + "\n")
			i++
			j++
			continue
		}
		k := -1
		for _, p := range positions[a[i]] {
			if p >= j {
				k = p
				break
			}
		}
		if k < 0 {
			out.WriteString("-" + a[i] + "\n")
			i++
			continue
		}
		for j < k {
			out.WriteString("+" + b[j] + "\n")
			j++
		}
	}
	for ; j < len(b); j++ {
		out.WriteString("+" + b[j] + "\n")
	}
	return out.String()
}

func contractPending(s contractstate.State) bool {
	for path := range s.Roots {
		if s.Pending(path) {
			return true
		}
	}
	return false
}

func (a app) reconcileContractRoots(home, name, root string) ([]contractRootResult, error) {
	state, _, err := observeContract(home, name, root, false)
	if err != nil {
		return nil, err
	}
	if state.Active == nil {
		return nil, nil
	}
	doc, err := loadSingleRegisteredDoc(home, name)
	if err != nil {
		return nil, err
	}
	current, err := currentContractSnapshot(doc.ref)
	if err != nil {
		return nil, err
	}
	if current.Provenance == "unpublished" {
		return nil, nil
	}
	expected := *state.Active
	if current != expected {
		return nil, fmt.Errorf("contract changed before guidance reconciliation; retry")
	}
	roots := sortedContractRoots(state)
	results := []contractRootResult{}
	for _, r := range roots {
		root := r.Umbrella
		if root == "" {
			root = r.Path
		}

		if !state.Pending(r.Path) && !umbrellaGuidanceStale(r, doc) {
			continue
		}
		result := contractRootResult{Path: r.Path, Kind: r.Kind, Status: "current"}
		// Serialize output and its installed marker together. Otherwise two
		// reconcilers could report a newer write while an older writer wins.
		_, stateErr := contractstate.Update(home, name, func(s *contractstate.State) error {
			if s.Active == nil || *s.Active != expected {
				return fmt.Errorf("active contract changed; retry")
			}
			before, err := currentContractSnapshot(doc.ref)
			if err != nil {
				return err
			}
			if before != expected {
				return fmt.Errorf("contract changed before guidance write; retry")
			}
			var writeErr error
			expectedOutputHash := ""
			_, exists, rootErr := existingUmbrellaRootStatus(root, name)
			if rootErr != nil {
				writeErr = rootErr
			} else if !exists {
				writeErr = fmt.Errorf("umbrella no longer exists")
			}
			if writeErr == nil {
				writeErr = safeContractOutput(r.Path)
			}
			if writeErr != nil {
				// Preserve the failure below and continue with independent roots.
			} else if r.Kind == "umbrella" {
				opts, err := guidanceOptionsForSelectedRole(root, doc.doc)
				if err != nil {
					writeErr = err
				} else {
					content, composeErr := guidance.ComposeWithOptions(doc.ref.LocalPath, doc.doc, opts)
					if composeErr != nil {
						return composeErr
					}
					expectedOutputHash = contractstate.ContentHash(content)
					written, err := guidance.Ensure(root, doc.ref.LocalPath, doc.doc, opts)
					writeErr = err
					if err == nil && written.Status == "blocked" {
						writeErr = fmt.Errorf("%s", written.Message)
					}
				}
			} else {
				session, err := worksession.Load(root, r.SessionID)
				if err != nil {
					writeErr = err
				} else if session.Status != worksession.StatusActive || session.Path != r.Path {
					writeErr = fmt.Errorf("session is no longer active at this path; retry")
				} else if !pathWithinContractRoot(root, session.Path) {
					writeErr = fmt.Errorf("session path is outside umbrella")
				} else {
					context, err := sessionGuidanceContext(root, doc)
					if err != nil {
						writeErr = err
					} else {
						expectedOutputHash = contractstate.ContentHash(worksession.RenderGuidance(session, context))
						writeErr = worksession.EnsureGuidance(session, context)
					}
				}
			}
			if writeErr == nil {
				after, err := currentContractSnapshot(doc.ref)
				if err != nil {
					writeErr = err
				} else if after != expected {
					writeErr = fmt.Errorf("contract changed during guidance write; retry")
				}
			}
			outputHash := ""
			if writeErr == nil {
				outputHash, writeErr = contractstate.OutputHash(r.Path)
			}
			if writeErr == nil && outputHash != expectedOutputHash {
				writeErr = fmt.Errorf("guidance changed during installation; retry")
			}
			if writeErr != nil {
				result.Status = "pending"
				result.Error = writeErr.Error()
			}
			s.Register(r.Path, r.Kind, r.SessionID)
			s.Installed(r.Path, expected.Revision, result.Error)
			if result.Error == "" {
				stored := s.Roots[r.Path]
				stored.InstalledOutputSHA256 = outputHash
				s.Roots[r.Path] = stored
			}
			return nil

		})
		if stateErr != nil {
			result.Status = "pending"
			result.Error = stateErr.Error()
		}
		results = append(results, result)
	}
	return results, nil
}

// umbrellaGuidanceStale catches an umbrella AGENTS.md that an older writer
// replaced after its marker was recorded: setup and launch still write base
// guidance outside the state lock. Only managed, stale files are rewritten.
func umbrellaGuidanceStale(r contractstate.Root, doc registeredDoc) bool {
	if r.Kind != "umbrella" {
		return false
	}
	opts, err := guidanceOptionsForSelectedRole(r.Path, doc.doc)
	if err != nil {
		return false
	}
	check, err := guidance.CheckWithOptions(r.Path, doc.ref.LocalPath, doc.doc, opts)
	return err == nil && check.Status == "stale"
}

func pathWithinContractRoot(root, path string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sortedContractRoots(s contractstate.State) []contractstate.Root {
	roots := make([]contractstate.Root, 0, len(s.Roots))
	for _, r := range s.Roots {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Path < roots[j].Path })
	return roots
}

// Reconciliation failures remain visible and retryable without preventing a
// launch or best-effort refresh that otherwise passed its existing gates.
func (a app) reconcileContractRootsWarning(home, name, root string) {
	roots, err := a.reconcileContractRoots(home, name, root)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: contract reconciliation pending: %v\n", err)
	}
	for _, r := range roots {
		if r.Status == "pending" {
			fmt.Fprintf(a.stderr, "warning: contract reconciliation pending for %s: %s\n", r.Path, r.Error)
		}
	}
}

// Revisit previously observed umbrellas as well as the current command's root.
// Closed sessions disappear from the live inventory; unreadable inventories
// retain their prior markers for a later retry.
func knownContractRoots(name, current string, state contractstate.State) ([]contractstate.Root, error) {
	bases := map[string]bool{}
	if current != "" {
		bases[current] = true
	}
	for _, r := range state.Roots {
		if r.Kind == "umbrella" {
			bases[r.Path] = true
		}
	}
	var roots []contractstate.Root
	var firstErr error
	for base := range bases {
		_, exists, err := existingUmbrellaRootStatus(base, name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !exists {
			continue
		}
		found, err := contractRoots(base)
		roots = append(roots, found...)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return roots, firstErr
}

// Existing generated aliases are allowed only when CLAUDE.md points directly
// to AGENTS.md. Fan-out must not follow arbitrary symlinks into other files.
func safeContractOutput(root string) error {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	// macOS /tmp and /var are system aliases, so compare the parent after
	// resolving it while still refusing a symlink at the output root itself.
	info, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink output root %s", root)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(resolved, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if name != "CLAUDE.md" || target != "AGENTS.md" {
				return fmt.Errorf("refusing unexpected guidance symlink %s", path)
			}
		}
	}
	return nil
}

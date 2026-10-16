package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/recordsvc"
	"github.com/fluxinc/my-cli/internal/support"
)

// referencePart is what a service accepts for a reference namespace or id.
var referencePart = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,120}$`)

const supportLinkBodyLimit = 2000

type supportLinkResult struct {
	Record  string `json:"record"`
	Created bool   `json:"created"`
}

// runSupportLink notes a published support record on service-backed fleet
// records the caller names by stable id. Nothing is written unless the
// record's current file is byte-equal on its mount's default branch and
// every named record exists under exactly that id.
func (a app) runSupportLink(args []string) error {
	var opts meetingCommonOpts
	var records stringListFlag
	fs := newFlagSet("my support link", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	fs.Var(&records, "record", "stable id of a fleet record (repeatable)")
	valueFlags := meetingValueFlags()
	valueFlags["record"] = true
	rest, err := parseInterspersed(fs, args, valueFlags)
	if err != nil {
		return err
	}
	if len(rest) != 1 || len(records) == 0 {
		return fmt.Errorf("usage: my support link <id|path> --record <stable-id> [--record <stable-id>]")
	}
	svc, err := boundFleetService(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if svc == nil {
		return a.maybeJSONError(opts.jsonOut, errors.New("fleet is not bound to a service; there is nothing to link a support record to"))
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	rec, _, err := support.Get(roots, rest[0])
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	ref, err := supportReference(svc.manifest, rec)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	gitURL, err := workspaceGitURL(opts.home, svc.manifest, rec.Workspace)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := verifyPublished(rec.Path, gitURL); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}

	client, err := svc.connect()
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	ctx, cancel := serviceContext()
	defer cancel()
	// Every record is read, under exactly the id given, before any write.
	var ids []string
	current := map[string]recordsvc.Record{}
	for _, id := range records {
		id = strings.TrimSpace(id)
		if _, seen := current[id]; seen {
			continue
		}
		got, err := client.Get(ctx, id)
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, describeServiceError(svc, err))
		}
		if got.ID() != id {
			return a.maybeJSONError(opts.jsonOut, fmt.Errorf("%s is not a stable id in %s (it names %s); pass the stable id itself", id, svc.service.ID, got.ID()))
		}
		ids = append(ids, id)
		current[id] = got
	}

	body := supportLinkBody(rec)
	var results []supportLinkResult
	for _, id := range ids {
		key := supportLinkKey(svc.service.ID, id, ref)
		_, created, err := client.Link(ctx, id, key, ref, body, current[id].Revisions())
		var svcErr *recordsvc.Error
		if errors.As(err, &svcErr) && svcErr.Stale() {
			// Someone changed the record since it was read: read it again and
			// retry once. The key makes the retry safe.
			fresh, getErr := client.Get(ctx, id)
			if getErr != nil {
				return a.reportPartialLinks(opts.jsonOut, results, describeServiceError(svc, getErr))
			}
			_, created, err = client.Link(ctx, id, key, ref, body, fresh.Revisions())
		}
		if err != nil {
			return a.reportPartialLinks(opts.jsonOut, results, fmt.Errorf("%s: %w", id, describeServiceError(svc, err)))
		}
		results = append(results, supportLinkResult{Record: id, Created: created})
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Support   string              `json:"support"`
			Reference recordsvc.Reference `json:"reference"`
			Links     []supportLinkResult `json:"links"`
		}{Support: rec.ID, Reference: ref, Links: results})
	}
	for _, r := range results {
		state := "linked"
		if !r.Created {
			state = "already linked"
		}
		fmt.Fprintf(a.stdout, "%s: %s %s\n", r.Record, state, rec.ID)
	}
	return nil
}

// reportPartialLinks names the links already made before the failure, so a
// rerun (which replays them) is understood.
func (a app) reportPartialLinks(jsonOut bool, done []supportLinkResult, err error) error {
	if len(done) != 0 && !jsonOut {
		for _, r := range done {
			fmt.Fprintf(a.stdout, "%s: linked before the failure\n", r.Record)
		}
	}
	return a.maybeJSONError(jsonOut, fmt.Errorf("%w; rerun the same command to finish (links already made are kept)", err))
}

// supportReference names a support record exactly: the manifest and
// workspace it lives in, so two organizations' (or mounts') ids differ.
func supportReference(manifestName string, rec support.Record) (recordsvc.Reference, error) {
	ref := recordsvc.Reference{Namespace: manifestName + "." + rec.Workspace, System: "support", ID: rec.ID}
	if !referencePart.MatchString(ref.Namespace) || !referencePart.MatchString(ref.ID) {
		return ref, fmt.Errorf("support record %q in %s cannot be referenced: ids are lowercase letters, digits, '.', '_' or '-', at most 121 characters", rec.ID, ref.Namespace)
	}
	return ref, nil
}

// supportLinkKey is the same for every retry of one link, from any process:
// a hash of the service, the record and the full reference, JSON-framed so
// no two inputs collide.
func supportLinkKey(serviceID, recordID string, ref recordsvc.Reference) string {
	framed, _ := json.Marshal([]string{"my-support-link/v1", serviceID, recordID, ref.Namespace, ref.System, ref.ID})
	sum := sha256.Sum256(framed)
	return hex.EncodeToString(sum[:])
}

// supportLinkBody is the reference and title only, never the record's text.
func supportLinkBody(rec support.Record) string {
	body := "Support record " + rec.ID
	if title := strings.TrimSpace(rec.Title); title != "" {
		body += ": " + title
	}
	if runes := []rune(body); len(runes) > supportLinkBodyLimit {
		body = string(runes[:supportLinkBodyLimit])
	}
	return body
}

// workspaceGitURL is the canonical remote the manifest declares for a
// support record's workspace or mount.
func workspaceGitURL(home, manifestName, workspaceID string) (string, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return "", err
	}
	for _, doc := range docs {
		if url := declaredGitURL(doc.doc, workspaceID); url != "" {
			return url, nil
		}
	}
	return "", fmt.Errorf("workspace %q has no git_url in manifest %s, so its publication cannot be checked", workspaceID, manifestName)
}

func declaredGitURL(doc manifest.Document, id string) string {
	for _, w := range doc.Workspaces {
		if w.ID == id {
			return w.GitURL
		}
	}
	for _, m := range doc.Mounts {
		if m.ID == id {
			return m.GitURL
		}
	}
	return ""
}

// verifyPublished requires the file's current bytes to be on the canonical
// remote's default branch, freshly fetched. Being clean, committed or pushed
// to some other branch is not enough: only what teammates get by syncing
// counts as published.
func verifyPublished(path, gitURL string) error {
	unpublished := func(why string) error {
		return fmt.Errorf("%s is not published: %s. Publish it to the workspace's default branch first (my sync --push, or finish its session), then link", path, why)
	}
	dir := filepath.Dir(path)
	top, err := gitText(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return unpublished("it is not in a git checkout")
	}
	top, _ = filepath.EvalSymlinks(top)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(top, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return unpublished("it is outside its checkout")
	}
	head, err := gitText(top, "ls-remote", "--symref", gitURL, "HEAD")
	if err != nil {
		return fmt.Errorf("checking %s: %w", gitURL, err)
	}
	branch, commit := "", ""
	for _, line := range strings.Split(head, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD":
			branch = fields[1]
		case len(fields) == 2 && fields[1] == "HEAD":
			commit = fields[0]
		}
	}
	if !strings.HasPrefix(branch, "refs/heads/") || commit == "" {
		return fmt.Errorf("%s did not report its default branch", gitURL)
	}
	if _, err := gitText(top, "fetch", "--quiet", "--no-tags", gitURL, branch); err != nil {
		return fmt.Errorf("fetching %s from %s: %w", branch, gitURL, err)
	}
	remote, err := gitText(top, "rev-parse", "--verify", "--quiet", commit+":"+filepath.ToSlash(rel))
	if err != nil {
		return unpublished("it is not on " + strings.TrimPrefix(branch, "refs/heads/"))
	}
	local, err := gitText(top, "hash-object", "--", rel)
	if err != nil {
		return err
	}
	if local != remote {
		return unpublished("its current text differs from " + strings.TrimPrefix(branch, "refs/heads/"))
	}
	return nil
}

func gitText(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = manifest.GitEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) != 0 {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

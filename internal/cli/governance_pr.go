package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fluxinc/my-cli/internal/access"
	governancecheck "github.com/fluxinc/my-cli/internal/governance"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/safefs"
	"github.com/fluxinc/my-cli/internal/syncer"
)

type prospectiveCommit struct {
	Commit  string
	Tree    string
	Repo    string
	GitEnv  []string
	Cleanup func() error
}

func (a app) pullRequestPublisher(home string) syncer.PRPublisher {
	return func(request syncer.PRRequest) syncer.PRResult {
		return a.publishPullRequest(home, request, false)
	}
}

func (a app) pullRequestPublisherAllowManual(home string) syncer.PRPublisher {
	return func(request syncer.PRRequest) syncer.PRResult {
		return a.publishPullRequest(home, request, true)
	}
}

func (a app) publishPullRequest(home string, request syncer.PRRequest, allowManualDomains bool) syncer.PRResult {
	fail := func(reason, message, next string) syncer.PRResult {
		return syncer.PRResult{Status: "failed", ReasonCode: reason, Error: message, NextCommand: next}
	}
	doc, err := loadSingleRegisteredDoc(home, request.Entry.Manifest)
	if err != nil {
		return fail("manifest_unavailable", err.Error(), "my manifests sync "+request.Entry.Manifest)
	}
	if request.Entry.Role != "manifest" {
		domains := changedRecordDomains(doc.doc, request.Entry.ID, append(append([]string(nil), request.Dirty...), request.Changed...))
		for _, domain := range domains {
			if domain.Publish == "manual-pr" && !allowManualDomains {
				return syncer.PRResult{Status: "held back", ReasonCode: "manual_record_domain", Message: "record domain " + domain.ID + " requires explicit `my record flush --include-manual`", NextCommand: "my record flush --include-manual"}
			}
		}
		if !compatibleRecordDomainPolicies(domains) {
			return syncer.PRResult{Status: "held back", ReasonCode: "mixed_record_domain_policy", Message: "changed record domains have different review or publication policies; split the changes into separate PRs", NextCommand: "git -C " + shellQuote(request.Entry.LocalPath) + " status --short"}
		}
	}
	repository, ok := access.GitHubRepositoryName(request.Entry.GitURL)
	if !ok {
		return fail("unsupported_repository", "PR publish requires a GitHub repository", "my doctor")
	}
	decision := access.ResolveGitHub(repository, a.accessRunner)
	if decision.State != access.StateAllowed || decision.Actor.ID == 0 || decision.Actor.NodeID == "" {
		return fail("pr_actor_unverified", fmt.Sprintf("cannot establish current GitHub actor and repository access: %s", decision.ReasonCode), "gh auth status")
	}
	publishRunner := a.publishRunner
	if publishRunner == nil {
		publishRunner = governanceExec
	}
	actor, err := currentPublishActor(publishRunner)
	if err != nil {
		return fail("pr_actor_unverified", err.Error(), "gh auth status")
	}
	if actor.ID != decision.Actor.ID || actor.NodeID != decision.Actor.NodeID {
		return fail("pr_actor_mismatch", "GitHub identity used for PR publication does not match the access-authorized actor", "gh auth status")
	}

	prospective, err := buildProspectiveCommit(request)
	if err != nil {
		return fail("prospective_commit_failed", err.Error(), "git -C "+shellQuote(request.Entry.LocalPath)+" status --short")
	}
	defer prospective.Cleanup()

	manifestBase, err := gitPRText(request.Entry.LocalPath, nil, "-C", doc.ref.LocalPath, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return fail("manifest_base_unavailable", err.Error(), "my manifests sync "+request.Entry.Manifest)
	}
	mount := request.Entry.ID
	if request.Entry.Role == "manifest" {
		mount = governancecheck.ManifestSurface
	}
	if manifest.GovernanceConfigured(doc.doc.Governance) {
		report, err := governancecheck.Check(governancecheck.CheckInput{
			Repo: request.Entry.LocalPath, Repository: repository,
			BaseRef: request.Upstream, HeadRef: prospective.Commit,
			ManifestRepo: doc.ref.LocalPath, ManifestBaseRef: manifestBase,
			Mount: mount, ActorID: actor.ID, ActorLogin: actor.Login,
			Runner: governancePRRunner(publishRunner, prospective),
		})
		if err != nil {
			return fail("governance_check_error", err.Error(), governanceNextCommand(request, doc, repository, mount, actor))
		}
		if !report.Allowed {
			codes := governanceViolationCodes(report)
			return syncer.PRResult{
				Status: "held back", ReasonCode: "governance_denied",
				Message:     "local governance pre-check denied change: " + strings.Join(codes, ","),
				NextCommand: governanceNextCommand(request, doc, repository, mount, actor),
			}
		}
	}
	changed := append([]string(nil), request.Dirty...)
	changed = append(changed, request.Changed...)
	changed = uniqueStrings(changed)
	if request.DryRun {
		autoMerge := " and wait for required checks and human approvals"
		if doc.doc.Sync.PullRequestAutoMerge {
			autoMerge = " and request gated auto-merge"
		}
		return syncer.PRResult{
			Status: "dry-run", Message: "governance pre-check passed; would create a dedicated branch, push it, open a pull request," + autoMerge,
			ReasonCode: "governance_pr_planned", Changed: changed,
		}
	}

	branch := governedBranchName(prospective.Commit)
	if _, err := gitPRBytes(request.Entry.LocalPath, nil, "update-ref", "refs/heads/"+branch, prospective.Commit); err != nil {
		return fail("pr_branch_failed", err.Error(), "git -C "+shellQuote(request.Entry.LocalPath)+" status --short")
	}
	if out, err := gitPRBytes(request.Entry.LocalPath, nil, "push", "origin", "refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		return fail("pr_push_failed", commandMessage(out, err), "git -C "+shellQuote(request.Entry.LocalPath)+" push origin "+branch)
	}
	if err := verifyRemoteBranch(request.Entry.LocalPath, branch, prospective.Commit); err != nil {
		return fail("pr_remote_proof_failed", err.Error(), "git -C "+shellQuote(request.Entry.LocalPath)+" ls-remote origin refs/heads/"+branch)
	}
	baseBranch := pullRequestBaseBranch(request.Upstream, request.Branch)
	body := fmt.Sprintf("Created by `my sync` from governed organization `%s`.\n\nTrusted manifest commit: `%s`\nProposed commit: `%s`\n\nThe repository's required governance check and review rules remain authoritative.", doc.doc.Organization.ID, manifestBase, prospective.Commit)
	prURL, found, err := findOpenPullRequest(publishRunner, repository, branch, baseBranch, actor.ID, prospective.Commit)
	if err != nil {
		return fail("pr_existing_proof_failed", err.Error(), "gh pr list --repo "+repository+" --head "+branch)
	}
	if !found {
		out, createErr := publishRunner("gh", "pr", "create", "--repo", repository, "--head", branch, "--base", baseBranch, "--title", request.Message, "--body", body)
		if createErr == nil {
			prURL = strings.TrimSpace(string(out))
		} else {
			// The request may have succeeded even if the client lost its response.
			// Re-query by the stable branch before reporting failure so retries do
			// not create duplicate PRs or discard the recovery branch.
			prURL, found, err = findOpenPullRequest(publishRunner, repository, branch, baseBranch, actor.ID, prospective.Commit)
			if err != nil || !found {
				return fail("pr_create_failed", commandMessage(out, createErr), "gh pr create --repo "+repository+" --head "+branch+" --base "+baseBranch)
			}
		}
	}
	if err := verifyPullRequest(publishRunner, repository, prURL, actor.ID, prospective.Commit); err != nil {
		return fail("pr_proof_failed", err.Error(), "gh pr view "+shellQuote(prURL))
	}

	// The commit and PR are now independently proven remote. Attach HEAD to the
	// dedicated topic branch and reset only the index. Neither command writes or
	// removes working-tree files. The former base ref is restored to its proven
	// upstream commit so an unmerged or squash-merged PR cannot make the local
	// default branch falsely appear ahead or cause accidental stacked changes.
	if err := attachPublishedTopic(request, branch, prospective.Commit); err != nil {
		return syncer.PRResult{Status: "pull request opened", Message: prURL + "; local checkout could not be marked committed: " + err.Error(), ReasonCode: "pr_open_local_unreconciled", NextCommand: "git -C " + shellQuote(request.Entry.LocalPath) + " status --short", Changed: changed, PRURL: prURL, PRHeadSHA: prospective.Commit, PRBase: baseBranch}
	}

	message := prURL + "; remote branch and immutable PR author verified"
	if !doc.doc.Sync.PullRequestAutoMerge {
		message += "; awaiting required checks and human approvals"
		return syncer.PRResult{Status: "pull request opened", Message: message, ReasonCode: "governance_pr_opened", NextCommand: "gh pr view " + shellQuote(prURL), Changed: changed, PRURL: prURL, PRHeadSHA: prospective.Commit, PRBase: baseBranch}
	}
	if mergeOut, mergeErr := publishRunner("gh", "pr", "merge", "--auto", "--merge", prURL); mergeErr != nil {
		message += "; requested auto-merge could not be enabled: " + commandMessage(mergeOut, mergeErr)
		return syncer.PRResult{Status: "pull request opened", Message: message, ReasonCode: "pr_open_auto_merge_pending", NextCommand: "gh pr view " + shellQuote(prURL), Changed: changed, PRURL: prURL, PRHeadSHA: prospective.Commit, PRBase: baseBranch}
	}
	message += "; auto-merge requested by manifest policy, still subject to required checks and reviews"
	return syncer.PRResult{Status: "pull request opened", Message: message, ReasonCode: "governance_pr_opened", NextCommand: "gh pr view " + shellQuote(prURL), Changed: changed, PRURL: prURL, PRHeadSHA: prospective.Commit, PRBase: baseBranch}
}

func changedRecordDomains(doc manifest.Document, mount string, paths []string) []manifest.RecordDomain {
	seen := map[string]bool{}
	var out []manifest.RecordDomain
	for _, domain := range doc.Governance.RecordDomains {
		if domain.Mount != mount {
			continue
		}
		for _, path := range paths {
			path = filepath.ToSlash(filepath.Clean(path))
			prefix := strings.Trim(filepath.ToSlash(domain.Path), "/")
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				if !seen[domain.ID] {
					out = append(out, domain)
					seen[domain.ID] = true
				}
				break
			}
		}
	}
	return out
}

func compatibleRecordDomainPolicies(domains []manifest.RecordDomain) bool {
	if len(domains) < 2 {
		return true
	}
	review, publish := domains[0].Review, domains[0].Publish
	for _, domain := range domains[1:] {
		if domain.Review != review || domain.Publish != publish {
			return false
		}
	}
	return true
}

func buildProspectiveCommit(request syncer.PRRequest) (prospectiveCommit, error) {
	if request.Head == "" || request.Upstream == "" || request.Branch == "" {
		return prospectiveCommit{}, fmt.Errorf("PR preflight is missing branch, upstream, or head identity")
	}
	if len(request.Entry.ContentPaths) == 0 {
		return prospectiveCommit{}, fmt.Errorf("PR publish requires declared content paths")
	}
	gitDir, err := gitPRText(request.Entry.LocalPath, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return prospectiveCommit{}, err
	}
	tempRoot := filepath.Join(gitDir, "my-cli", "tmp")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return prospectiveCommit{}, err
	}
	tempDir, err := os.MkdirTemp(tempRoot, "pr-")
	if err != nil {
		return prospectiveCommit{}, err
	}
	cleanup := func() error { return safefs.RemoveAll(tempDir) }
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tempDir, "index")}
	if request.DryRun {
		objectDir := filepath.Join(tempDir, "objects")
		if err := os.MkdirAll(objectDir, 0o700); err != nil {
			_ = cleanup()
			return prospectiveCommit{}, err
		}
		mainObjects, err := gitPRText(request.Entry.LocalPath, nil, "rev-parse", "--git-path", "objects")
		if err != nil {
			_ = cleanup()
			return prospectiveCommit{}, err
		}
		if !filepath.IsAbs(mainObjects) {
			mainObjects = filepath.Join(request.Entry.LocalPath, mainObjects)
		}
		env = append(env, "GIT_OBJECT_DIRECTORY="+objectDir, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+mainObjects)
	}
	if _, err := gitPRBytes(request.Entry.LocalPath, env, "read-tree", request.Head); err != nil {
		_ = cleanup()
		return prospectiveCommit{}, err
	}
	if len(request.Dirty) != 0 {
		args := []string{"add", "-A", "--"}
		args = append(args, request.Dirty...)
		if _, err := gitPRBytes(request.Entry.LocalPath, env, args...); err != nil {
			_ = cleanup()
			return prospectiveCommit{}, err
		}
	}
	tree, err := gitPRText(request.Entry.LocalPath, env, "write-tree")
	if err != nil {
		_ = cleanup()
		return prospectiveCommit{}, err
	}
	baseTree, err := gitPRText(request.Entry.LocalPath, env, "rev-parse", request.Head+"^{tree}")
	if err != nil {
		_ = cleanup()
		return prospectiveCommit{}, err
	}
	if tree == baseTree && len(request.Changed) == 0 {
		_ = cleanup()
		return prospectiveCommit{}, fmt.Errorf("no publishable changes inside declared content paths")
	}
	if len(request.Dirty) == 0 {
		// Existing ahead commits are already the exact proposal. Do not add an
		// empty synthetic commit merely to obtain a PR head.
		return prospectiveCommit{Commit: request.Head, Tree: tree, Repo: request.Entry.LocalPath, GitEnv: env, Cleanup: cleanup}, nil
	}
	parentDate, err := gitPRText(request.Entry.LocalPath, env, "show", "-s", "--format=%cI", request.Head)
	if err != nil {
		_ = cleanup()
		return prospectiveCommit{}, err
	}
	commitEnv := append(append([]string(nil), env...), "GIT_AUTHOR_DATE="+parentDate, "GIT_COMMITTER_DATE="+parentDate)
	commit, err := gitPRText(request.Entry.LocalPath, commitEnv, "commit-tree", tree, "-p", request.Head, "-m", request.Message)
	if err != nil {
		_ = cleanup()
		return prospectiveCommit{}, err
	}
	return prospectiveCommit{Commit: commit, Tree: tree, Repo: request.Entry.LocalPath, GitEnv: env, Cleanup: cleanup}, nil
}

func governancePRRunner(publishRunner manifest.Runner, prospective prospectiveCommit) governancecheck.Runner {
	return func(name string, args ...string) ([]byte, error) {
		if name == "git" {
			repo := ""
			gitArgs := args
			if len(args) >= 2 && args[0] == "-C" {
				repo, gitArgs = args[1], args[2:]
			}
			env := []string(nil)
			// Dry-run objects live outside the repository object database. Only
			// commands against the proposed content repo receive that alternate
			// object environment; the manifest repo must use its own object store.
			if samePath(repo, prospective.Repo) {
				env = prospective.GitEnv
			}
			return gitPRBytes(repo, env, gitArgs...)
		}
		return publishRunner(name, args...)
	}
}

type publishActor struct {
	ID     int64  `json:"id"`
	NodeID string `json:"node_id"`
	Login  string `json:"login"`
}

func currentPublishActor(runner manifest.Runner) (publishActor, error) {
	out, err := runner("gh", "api", "user")
	if err != nil {
		return publishActor{}, fmt.Errorf("resolve PR publishing identity: %s", commandMessage(out, err))
	}
	var actor publishActor
	if err := json.Unmarshal(out, &actor); err != nil || actor.ID <= 0 || actor.NodeID == "" || actor.Login == "" {
		return publishActor{}, fmt.Errorf("resolve PR publishing identity: incomplete immutable GitHub identity")
	}
	return actor, nil
}

func governedBranchName(commit string) string {
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	return "my/governed/" + short
}

func attachPublishedTopic(request syncer.PRRequest, branch, commit string) error {
	currentBranch, err := gitPRText(request.Entry.LocalPath, nil, "symbolic-ref", "--short", "HEAD")
	if err != nil || currentBranch != request.Branch {
		return fmt.Errorf("checkout branch moved after preflight")
	}
	currentHead, err := gitPRText(request.Entry.LocalPath, nil, "rev-parse", "HEAD^{commit}")
	if err != nil || currentHead != request.Head {
		return fmt.Errorf("checkout head moved after preflight")
	}
	upstreamCommit, err := gitPRText(request.Entry.LocalPath, nil, "rev-parse", request.Upstream+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve preflight upstream: %w", err)
	}
	if _, err := gitPRBytes(request.Entry.LocalPath, nil, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		return err
	}
	if _, err := gitPRBytes(request.Entry.LocalPath, nil, "reset", "--mixed", commit); err != nil {
		return err
	}
	if _, err := gitPRBytes(request.Entry.LocalPath, nil, "update-ref", "refs/heads/"+request.Branch, upstreamCommit, request.Head); err != nil {
		return fmt.Errorf("restore local base ref: %w", err)
	}
	return nil
}

func pullRequestBaseBranch(upstream, fallback string) string {
	if slash := strings.IndexByte(upstream, '/'); slash >= 0 && slash+1 < len(upstream) {
		return upstream[slash+1:]
	}
	return fallback
}

func verifyRemoteBranch(repo, branch, commit string) error {
	out, err := gitPRText(repo, nil, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		return err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != commit || fields[1] != "refs/heads/"+branch {
		return fmt.Errorf("remote branch proof does not match proposed commit")
	}
	return nil
}

type pullRequestProof struct {
	HTMLURL string `json:"html_url"`
	User    struct {
		ID int64 `json:"id"`
	} `json:"user"`
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

func findOpenPullRequest(runner manifest.Runner, repository, branch, base string, actorID int64, commit string) (string, bool, error) {
	owner := strings.SplitN(repository, "/", 2)[0]
	endpoint := "repos/" + repository + "/pulls?state=open&head=" + url.QueryEscape(owner+":"+branch) + "&base=" + url.QueryEscape(base)
	out, err := runner("gh", "api", endpoint)
	if err != nil {
		return "", false, fmt.Errorf("query existing pull request: %s", commandMessage(out, err))
	}
	var proofs []pullRequestProof
	if err := json.Unmarshal(out, &proofs); err != nil {
		return "", false, fmt.Errorf("query existing pull request: %w", err)
	}
	if len(proofs) == 0 {
		return "", false, nil
	}
	if len(proofs) != 1 || proofs[0].HTMLURL == "" || proofs[0].User.ID != actorID || proofs[0].Head.SHA != commit {
		return "", false, fmt.Errorf("existing pull request for governed branch does not match the publishing actor and proposed commit")
	}
	return proofs[0].HTMLURL, true, nil
}

func verifyPullRequest(runner manifest.Runner, repository, url string, actorID int64, commit string) error {
	number, err := pullRequestNumber(url)
	if err != nil {
		return err
	}
	out, err := runner("gh", "api", "repos/"+repository+"/pulls/"+strconv.Itoa(number))
	if err != nil {
		return fmt.Errorf("verify pull request: %s", commandMessage(out, err))
	}
	var proof pullRequestProof
	if err := json.Unmarshal(out, &proof); err != nil {
		return fmt.Errorf("verify pull request: %w", err)
	}
	if proof.User.ID != actorID {
		return fmt.Errorf("pull request author id %d does not match publishing actor id %d", proof.User.ID, actorID)
	}
	if proof.Head.SHA != commit {
		return fmt.Errorf("pull request head %s does not match proposed commit %s", proof.Head.SHA, commit)
	}
	if proof.HTMLURL != "" && strings.TrimSuffix(proof.HTMLURL, "/") != strings.TrimSuffix(url, "/") {
		return fmt.Errorf("pull request URL proof does not match created URL")
	}
	return nil
}

func pullRequestNumber(url string) (int, error) {
	clean := strings.TrimSuffix(strings.TrimSpace(url), "/")
	part := clean[strings.LastIndex(clean, "/")+1:]
	number, err := strconv.Atoi(part)
	if err != nil || number <= 0 || !strings.Contains(clean, "/pull/") {
		return 0, fmt.Errorf("gh pr create returned an invalid pull request URL: %q", url)
	}
	return number, nil
}

func governanceViolationCodes(report governancecheck.Report) []string {
	var codes []string
	for _, violation := range report.Violations {
		if !stringSliceContains(codes, violation.ReasonCode) {
			codes = append(codes, violation.ReasonCode)
		}
	}
	return codes
}

func governanceNextCommand(request syncer.PRRequest, doc registeredDoc, repository, mount string, actor publishActor) string {
	return fmt.Sprintf("my governance check --repo %s --repository %s --base %s --head <proposed-commit> --manifest-repo %s --manifest-base HEAD --mount %s --actor-id %d --actor-login %s", shellQuote(request.Entry.LocalPath), repository, shellQuote(request.Upstream), shellQuote(doc.ref.LocalPath), shellQuote(mount), actor.ID, shellQuote(actor.Login))
}

func gitPRText(repo string, env []string, args ...string) (string, error) {
	out, err := gitPRBytes(repo, env, args...)
	return strings.TrimSpace(string(out)), err
}

func gitPRBytes(repo string, env []string, args ...string) ([]byte, error) {
	command := args
	if repo != "" && (len(args) < 2 || args[0] != "-C") {
		command = append([]string{"-C", repo}, args...)
	}
	cmd := exec.Command("git", command...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %s", strings.Join(args, " "), commandMessage(out, err))
	}
	return out, nil
}

func commandMessage(out []byte, err error) string {
	message := strings.TrimSpace(string(out))
	if message == "" && err != nil {
		message = err.Error()
	}
	return message
}

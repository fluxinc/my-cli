package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
)

type governanceAuditReport struct {
	Compliant    bool                        `json:"compliant"`
	Organization string                      `json:"organization"`
	Manifest     string                      `json:"manifest"`
	Repositories []governanceRepositoryAudit `json:"repositories"`
}

type governanceRepositoryAudit struct {
	Repository    string                 `json:"repository"`
	Surface       string                 `json:"surface"`
	DefaultBranch string                 `json:"default_branch"`
	Compliant     bool                   `json:"compliant"`
	Checks        []governanceAuditCheck `json:"checks"`
}

type governanceAuditCheck struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type githubRepositoryAuditInfo struct {
	DefaultBranch string `json:"default_branch"`
}

type githubRuleset struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Target       string            `json:"target"`
	Enforcement  string            `json:"enforcement"`
	BypassActors []json.RawMessage `json:"bypass_actors"`
	Conditions   struct {
		RefName struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []githubRule `json:"rules"`
}

type githubRule struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
}

type githubContentResponse struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func (a app) runGovernanceAudit(args []string) error {
	var home, manifestName, umbrellaRoot string
	var jsonOut bool
	fs := newFlagSet("my governance audit", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "audit one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "umbrella": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("governance audit does not accept positional arguments")
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	if !manifest.GovernanceConfigured(doc.doc.Governance) {
		return fmt.Errorf("manifest %q does not configure governance", manifestName)
	}
	runner := a.publishRunner
	if runner == nil {
		runner = governanceExec
	}
	surfaces, err := governedAuditSurfaces(doc.doc)
	if err != nil {
		return err
	}
	report := governanceAuditReport{Compliant: true, Organization: doc.doc.Organization.ID, Manifest: manifestName}
	for _, surface := range surfaces {
		audit := auditGovernedRepository(runner, surface.Repository, surface.Surface, surface.CodeownerPaths...)
		report.Repositories = append(report.Repositories, audit)
		if !audit.Compliant {
			report.Compliant = false
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, report); err != nil {
			return err
		}
	} else {
		for _, repository := range report.Repositories {
			status := "compliant"
			if !repository.Compliant {
				status = "noncompliant"
			}
			fmt.Fprintf(a.stdout, "repository\t%s\t%s\t%s\n", repository.Repository, repository.Surface, status)
			for _, check := range repository.Checks {
				checkStatus := "ok"
				if !check.OK {
					checkStatus = "missing"
				}
				fmt.Fprintf(a.stdout, "check\t%s\t%s\t%s\n", check.ID, checkStatus, check.Message)
			}
		}
	}
	if !report.Compliant {
		return fmt.Errorf("governance audit found missing GitHub enforcement; configure active rulesets, required reviews/checks, and workflow CODEOWNERS before release")
	}
	return nil
}

type governedAuditSurface struct {
	Repository     string
	Surface        string
	CodeownerPaths []string
}

func governedAuditSurfaces(doc manifest.Document) ([]governedAuditSurface, error) {
	manifestRepo, ok := access.GitHubRepositoryName(doc.Governance.Authorization.ManifestRepository)
	if !ok {
		return nil, fmt.Errorf("invalid governed manifest repository")
	}
	surfaces := []governedAuditSurface{{Repository: manifestRepo, Surface: "@manifest"}}
	protected := map[string]bool{}
	for _, protection := range manifest.GovernanceProtections(doc.Governance) {
		protected[protection.Mount] = true
	}
	codeownerPaths := map[string][]string{}
	for _, domain := range doc.Governance.RecordDomains {
		if domain.Review == "codeowner" {
			codeownerPaths[domain.Mount] = append(codeownerPaths[domain.Mount], domain.Path)
		}
	}
	for _, mount := range manifest.EffectiveMounts(doc) {
		if !protected[mount.ID] {
			continue
		}
		repository, ok := access.GitHubRepositoryName(mount.GitURL)
		if !ok {
			return nil, fmt.Errorf("protected mount %q is not a GitHub repository", mount.ID)
		}
		surfaces = append(surfaces, governedAuditSurface{Repository: repository, Surface: mount.ID, CodeownerPaths: codeownerPaths[mount.ID]})
	}
	sort.Slice(surfaces, func(i, j int) bool { return surfaces[i].Repository < surfaces[j].Repository })
	return surfaces, nil
}

func auditGovernedRepository(runner manifest.Runner, repository, surface string, codeownerPaths ...string) governanceRepositoryAudit {
	audit := governanceRepositoryAudit{Repository: repository, Surface: surface, Compliant: true}
	add := func(id string, ok bool, message string) {
		audit.Checks = append(audit.Checks, governanceAuditCheck{ID: id, OK: ok, Message: message})
		if !ok {
			audit.Compliant = false
		}
	}
	out, err := runner("gh", "api", "repos/"+repository)
	if err != nil {
		add("repository-readable", false, commandMessage(out, err))
		return audit
	}
	var info githubRepositoryAuditInfo
	if err := json.Unmarshal(out, &info); err != nil || info.DefaultBranch == "" {
		add("repository-readable", false, "default branch could not be resolved")
		return audit
	}
	audit.DefaultBranch = info.DefaultBranch
	add("repository-readable", true, "default branch "+info.DefaultBranch)

	rulesets, err := loadDetailedRulesets(runner, repository)
	if err != nil {
		add("active-ruleset", false, err.Error())
		return audit
	}
	active := applicableActiveRulesets(rulesets, info.DefaultBranch)
	add("active-ruleset", len(active) != 0, fmt.Sprintf("%d active default-branch ruleset(s)", len(active)))
	var hasPR, hasReview, hasCodeOwner, dismissesStale, resolvesThreads, hasCheck, hasDeletion, hasNonFF bool
	noBypass := true
	for _, ruleset := range active {
		if len(ruleset.BypassActors) != 0 {
			noBypass = false
		}
		for _, rule := range ruleset.Rules {
			switch rule.Type {
			case "pull_request":
				hasPR = true
				var params struct {
					RequiredApprovingReviewCount   int  `json:"required_approving_review_count"`
					RequireCodeOwnerReview         bool `json:"require_code_owner_review"`
					DismissStaleReviewsOnPush      bool `json:"dismiss_stale_reviews_on_push"`
					RequiredReviewThreadResolution bool `json:"required_review_thread_resolution"`
				}
				_ = json.Unmarshal(rule.Parameters, &params)
				hasReview = hasReview || params.RequiredApprovingReviewCount >= 1
				hasCodeOwner = hasCodeOwner || params.RequireCodeOwnerReview
				dismissesStale = dismissesStale || params.DismissStaleReviewsOnPush
				resolvesThreads = resolvesThreads || params.RequiredReviewThreadResolution
			case "required_status_checks":
				var params struct {
					RequiredStatusChecks []struct {
						Context string `json:"context"`
					} `json:"required_status_checks"`
				}
				_ = json.Unmarshal(rule.Parameters, &params)
				for _, check := range params.RequiredStatusChecks {
					hasCheck = hasCheck || check.Context == "my-governance"
				}
			case "deletion":
				hasDeletion = true
			case "non_fast_forward":
				hasNonFF = true
			}
		}
	}
	add("pull-request-required", hasPR, "default branch requires pull requests")
	add("approval-required", hasReview, "at least one approving review is required")
	add("code-owner-review", hasCodeOwner, "code-owner review is required")
	add("stale-approvals-dismissed", dismissesStale, "new commits dismiss stale approvals")
	add("conversations-resolved", resolvesThreads, "review conversations must be resolved")
	add("my-governance-required", hasCheck, "required status check is named my-governance")
	add("branch-deletion-blocked", hasDeletion, "ruleset blocks default-branch deletion")
	add("force-push-blocked", hasNonFF, "ruleset blocks non-fast-forward updates")
	add("no-bypass-actors", noBypass && len(active) != 0, "governance rulesets have no bypass actors")

	workflow, workflowErr := readGitHubContent(runner, repository, info.DefaultBranch, ".github/workflows/my-governance.yml")
	workflowOK := workflowErr == nil && workflowUsesImmutablePRAuthor(workflow) && workflowUsesPinnedActions(workflow) && workflowBuildsPinnedValidator(workflow)
	message := "workflow binds pull_request.user.id/login, pins external actions, and builds a commit-pinned validator"
	if workflowErr != nil {
		message = workflowErr.Error()
	}
	add("trusted-workflow", workflowOK, message)
	owners, ownersErr := readGitHubContent(runner, repository, info.DefaultBranch, ".github/CODEOWNERS")
	ownersOK := ownersErr == nil && codeownersProtectsGovernance(owners)
	message = "CODEOWNERS covers .github/workflows and CODEOWNERS itself"
	if ownersErr != nil {
		message = ownersErr.Error()
	}
	add("workflow-codeowners", ownersOK, message)
	if len(codeownerPaths) != 0 {
		var missing []string
		for _, path := range codeownerPaths {
			if !codeownersCoversDomain(owners, path) {
				missing = append(missing, path)
			}
		}
		add("domain-codeowners", ownersErr == nil && len(missing) == 0, "CODEOWNERS covers review-required domain paths; missing: "+strings.Join(missing, ","))
	}
	return audit
}

func loadDetailedRulesets(runner manifest.Runner, repository string) ([]githubRuleset, error) {
	out, err := runner("gh", "api", "repos/"+repository+"/rulesets")
	if err != nil {
		return nil, fmt.Errorf("list repository rulesets: %s", commandMessage(out, err))
	}
	var summaries []githubRuleset
	if err := json.Unmarshal(out, &summaries); err != nil {
		return nil, fmt.Errorf("parse repository rulesets: %w", err)
	}
	var detailed []githubRuleset
	for _, summary := range summaries {
		if summary.ID <= 0 {
			continue
		}
		out, err := runner("gh", "api", fmt.Sprintf("repos/%s/rulesets/%d", repository, summary.ID))
		if err != nil {
			return nil, fmt.Errorf("read repository ruleset %d: %s", summary.ID, commandMessage(out, err))
		}
		var ruleset githubRuleset
		if err := json.Unmarshal(out, &ruleset); err != nil {
			return nil, fmt.Errorf("parse repository ruleset %d: %w", summary.ID, err)
		}
		detailed = append(detailed, ruleset)
	}
	return detailed, nil
}

func applicableActiveRulesets(rulesets []githubRuleset, defaultBranch string) []githubRuleset {
	var out []githubRuleset
	for _, ruleset := range rulesets {
		if ruleset.Enforcement != "active" || (ruleset.Target != "branch" && ruleset.Target != "") {
			continue
		}
		for _, include := range ruleset.Conditions.RefName.Include {
			if include == "~DEFAULT_BRANCH" || include == defaultBranch || include == "refs/heads/"+defaultBranch {
				out = append(out, ruleset)
				break
			}
		}
	}
	return out
}

func readGitHubContent(runner manifest.Runner, repository, branch, path string) (string, error) {
	out, err := runner("gh", "api", "repos/"+repository+"/contents/"+path+"?ref="+branch)
	if err != nil {
		return "", fmt.Errorf("read %s: %s", path, commandMessage(out, err))
	}
	var response githubContentResponse
	if err := json.Unmarshal(out, &response); err != nil {
		return "", err
	}
	if response.Encoding != "base64" {
		return "", fmt.Errorf("read %s: unsupported content encoding", path)
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func workflowUsesImmutablePRAuthor(body string) bool {
	return strings.Contains(body, "pull_request_target:") &&
		strings.Contains(body, "github.event.pull_request.user.id") &&
		strings.Contains(body, "github.event.pull_request.user.login") &&
		strings.Contains(body, "governance check") && strings.Contains(body, "--manifest-base") &&
		strings.Contains(body, "--attestation-repository") && strings.Contains(body, "--attestation-base") &&
		strings.Contains(body, "github.event.pull_request.number") &&
		strings.Contains(body, "--pull-request-number") && strings.Contains(body, "--record-repository") &&
		strings.Contains(body, "--record-base")
}

func workflowBuildsPinnedValidator(body string) bool {
	return strings.Contains(body, "repository: fluxinc/my-cli") &&
		strings.Contains(body, "MY_CLI_COMMIT") &&
		strings.Contains(body, `^[0-9a-f]{40}$`) &&
		strings.Contains(body, "git -C my-cli rev-parse HEAD^{commit}") &&
		strings.Contains(body, "go build") &&
		strings.Contains(body, `"$RUNNER_TEMP/my-governance" governance check`)
}

var pinnedActionPattern = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*[^#\s]+@([0-9a-fA-F]{40})\s*(?:#.*)?$`)

func workflowUsesPinnedActions(body string) bool {
	usesLines := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(strings.TrimSpace(line), "uses:") {
			usesLines++
		}
	}
	return usesLines > 0 && len(pinnedActionPattern.FindAllStringSubmatch(body, -1)) == usesLines
}

func codeownersProtectsGovernance(body string) bool {
	var workflows, codeowners bool
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "/.github/", ".github/", "/.github/*", "/.github/**":
			workflows, codeowners = true, true
		case "/.github/workflows/", ".github/workflows/", "/.github/workflows/*", "/.github/workflows/**":
			workflows = true
		case "/.github/CODEOWNERS", ".github/CODEOWNERS":
			codeowners = true
		}
	}
	return workflows && codeowners
}

func codeownersCoversDomain(body, domainPath string) bool {
	domainPath = strings.Trim(filepath.ToSlash(domainPath), "/")
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pattern := strings.TrimSpace(fields[0])
		switch pattern {
		case "*", "/*", "/**":
			return true
		}
		clean := strings.Trim(strings.TrimSuffix(strings.TrimSuffix(pattern, "**"), "*"), "/")
		if clean == domainPath || strings.HasPrefix(domainPath, clean+"/") {
			return true
		}
	}
	return false
}

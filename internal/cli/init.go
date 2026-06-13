package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/umbrella"
)

type initOptions struct {
	orgID        string
	orgName      string
	repoPath     string
	home         string
	umbrellaPath string
	setup        bool
	jsonOut      bool
}

type initResult struct {
	OrganizationID   string                `json:"organization_id"`
	OrganizationName string                `json:"organization_name"`
	RepoPath         string                `json:"repo_path"`
	ContentPath      string                `json:"content_path"`
	Manifest         manifest.Ref          `json:"manifest"`
	Sync             []manifest.SyncResult `json:"sync"`
	NextCommands     []initNextCommand     `json:"next_commands"`
}

type initNextCommand struct {
	Action  string `json:"action"`
	Command string `json:"command"`
}

func (a app) runInit(args []string) error {
	var opts initOptions
	fs := newFlagSet("our init", a.stderr)
	fs.StringVar(&opts.orgName, "name", "", "organization display name")
	fs.StringVar(&opts.repoPath, "path", "", "content repository path")
	fs.StringVar(&opts.umbrellaPath, "umbrella", "", "recommended umbrella path")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.BoolVar(&opts.setup, "setup", false, "run our setup after creating and syncing the manifest")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		a.printInitUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"name":     true,
		"path":     true,
		"umbrella": true,
		"home":     true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our init <org-id>")
	}
	if opts.setup && opts.jsonOut {
		return fmt.Errorf("--setup and --json are not supported together")
	}
	opts.orgID = rest[0]
	result, err := a.createInitScaffold(opts)
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, result)
	}
	a.printInitResult(result)
	if opts.setup {
		setupArgs := []string{"--manifest", opts.orgID}
		if opts.home != "" {
			setupArgs = append(setupArgs, "--home", opts.home)
		}
		if opts.umbrellaPath != "" {
			setupArgs = append(setupArgs, "--umbrella", opts.umbrellaPath)
		}
		return a.runOnboard(setupArgs)
	}
	return nil
}

func (a app) printInitUsage() {
	fmt.Fprintln(a.stderr, `Usage of our init:
  our init <org-id> [--name NAME] [--path DIR] [--umbrella DIR] [--home DIR] [--setup] [--json]

Creates two local repositories and registers the organization: a private
manifest repository (the control plane: manifest, catalog, skills) and a
content repository mounted in the umbrella (the workspace handbook). Both are
local-only until our publish creates their remotes.

Examples:
  our init acme --name "Acme"
  our init acme --path ~/acme/handbook

Options:`)
}

func (a app) createInitScaffold(opts initOptions) (initResult, error) {
	if !portableKebab(opts.orgID) {
		return initResult{}, fmt.Errorf("organization id %q must be lowercase kebab-case", opts.orgID)
	}
	if _, exists, err := manifest.Find(opts.home, opts.orgID); err != nil {
		return initResult{}, err
	} else if exists {
		return initResult{}, fmt.Errorf("manifest %q is already registered", opts.orgID)
	}
	homeDir, err := resolveHome(opts.home)
	if err != nil {
		return initResult{}, err
	}
	umbrellaPath := strings.TrimSpace(opts.umbrellaPath)
	if umbrellaPath == "" {
		umbrellaPath = "~/" + opts.orgID
	}
	// Control plane: the private manifest repository at the registry path.
	manifestPath, err := manifest.DefaultCachePath(opts.home, opts.orgID)
	if err != nil {
		return initResult{}, err
	}
	// Data plane: the content repository mounted visibly in the umbrella.
	contentPath := opts.repoPath
	if contentPath == "" {
		contentPath = umbrella.MountPath(expandUserPath(homeDir, umbrellaPath), initMountID)
	} else {
		contentPath = expandUserPath(homeDir, contentPath)
	}
	contentPath, err = filepath.Abs(contentPath)
	if err != nil {
		return initResult{}, err
	}
	if err := ensureInitTarget(manifestPath); err != nil {
		return initResult{}, err
	}
	if err := ensureInitTarget(contentPath); err != nil {
		return initResult{}, err
	}
	orgName := strings.TrimSpace(opts.orgName)
	if orgName == "" {
		orgName = displayNameFromID(opts.orgID)
	}
	doc := initManifestDocument(opts.orgID, orgName, umbrellaPath, contentPath)
	if err := writeInitManifestScaffold(manifestPath, doc); err != nil {
		return initResult{}, err
	}
	if err := writeInitContentScaffold(contentPath, opts.orgID, orgName); err != nil {
		return initResult{}, err
	}
	if validation := manifest.ValidateFile(manifestPath); len(validation.Errors) != 0 {
		return initResult{}, fmt.Errorf("generated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	if err := gitInitCommit(manifestPath); err != nil {
		return initResult{}, err
	}
	if err := gitInitCommit(contentPath); err != nil {
		return initResult{}, err
	}
	ref, err := manifest.Add(opts.home, opts.orgID, manifestPath)
	if err != nil {
		return initResult{}, err
	}
	syncResults, err := manifest.Sync(opts.home, []string{opts.orgID}, false, false, nil)
	if err != nil {
		return initResult{}, err
	}
	if manifestResultsFailed(syncResults) {
		return initResult{}, fmt.Errorf("manifest sync failed")
	}
	result := initResult{
		OrganizationID:   opts.orgID,
		OrganizationName: orgName,
		RepoPath:         manifestPath,
		ContentPath:      contentPath,
		Manifest:         ref,
		Sync:             syncResults,
	}
	result.NextCommands = initNextCommands(opts.home, opts.orgID)
	return result, nil
}

// initMountID names the content mount our init declares: the org workspace,
// "the actual content" — distinct from the private manifest (control plane).
// The content repo lives at the umbrella mount path for this id and is
// published as <org>-workspace.

// initMountID names the content mount our init declares: the org workspace,
// "the actual content" — distinct from the private manifest (control plane).
// The content repo lives at the umbrella mount path for this id and is
// published as <org>-workspace.
const initMountID = "workspace"

func initManifestDocument(orgID, orgName, umbrellaPath, contentGitURL string) manifest.Document {
	return manifest.Document{
		ManifestVersion: 1,
		Organization: manifest.Organization{
			ID:   orgID,
			Name: orgName,
		},
		Umbrella: manifest.Umbrella{
			RecommendedPath: umbrellaPath,
		},
		AgentGuidance: manifest.AgentGuidance{
			Paths: []string{"agent-guidance/" + orgID + ".md"},
		},
		Skills: []manifest.Skill{
			{
				ID:          orgID + ":handbook",
				InstallSlug: orgID + "-handbook",
				Path:        "skills/" + orgID + "-handbook",
				Capabilities: []string{
					"meetings",
					"support",
					"fleet",
					"decisions",
					"workspace",
				},
				Requires: []string{"workspace:" + initMountID},
			},
		},
		Mounts: []manifest.Mount{
			{
				ID:   initMountID,
				Kind: "handbook",
				// Local until our publish rewrites it to the hosted remote.
				GitURL: contentGitURL,
				Mode:   "default",
			},
		},
	}
}

func ensureInitTarget(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("init path %s exists and is not a directory", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("init path %s is not empty", path)
	}
	return nil
}

func writeInitManifestScaffold(root string, doc manifest.Document) error {
	if err := manifest.SaveDocument(root, doc); err != nil {
		return err
	}
	orgID := doc.Organization.ID
	orgName := doc.Organization.Name
	files := map[string]string{
		"README.md": initManifestREADME(orgID, orgName),
		filepath.Join("agent-guidance", orgID+".md"):           initAgentGuidance(orgName),
		filepath.Join("skills", orgID+"-handbook", "SKILL.md"): initSkillDoc(orgID, orgName),
		filepath.Join("catalog", "products.json"):              "[]\n",
	}
	for path, content := range files {
		if err := writeInitFile(filepath.Join(root, path), content); err != nil {
			return err
		}
	}
	return nil
}

func writeInitContentScaffold(root, orgID, orgName string) error {
	files := map[string]string{
		"README.md":                             initContentREADME(orgName),
		filepath.Join("customers", "README.md"): initSectionREADME("Customers", "Keep customer identity records as one markdown file per customer."),
		filepath.Join("meetings", "README.md"):  initSectionREADME("Meetings", "Record meeting notes with our meetings add, then publish with our sync."),
		filepath.Join("support", "README.md"):   initSectionREADME("Support", "Record anonymized problem-to-solution notes with our support add."),
		filepath.Join("fleet", "README.md"):     initSectionREADME("Fleet", "Track deployed instances or devices with our fleet add and our fleet set."),
		filepath.Join("decisions", "README.md"): initSectionREADME("Decisions", "Keep durable decisions and their context here."),
		filepath.Join("projects", "README.md"):  initSectionREADME("Projects", "Keep project notes that agents should be able to read."),
		filepath.Join("policy", "README.md"):    initSectionREADME("Policy", "Keep operating policies and rules of engagement here."),
		filepath.Join("people", "README.md"):    initSectionREADME("People", "Keep public-safe team role notes here."),
	}
	for path, content := range files {
		if err := writeInitFile(filepath.Join(root, path), content); err != nil {
			return err
		}
	}
	return nil
}

func writeInitFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func initManifestREADME(orgID, orgName string) string {
	return fmt.Sprintf(`# %s Our AI Manifest

This private repository is the %s organization manifest: the control plane
that defines mounts, skills, the product/repo catalog, and agent guidance. Day-to-day
workspace content lives in the mounted content repositories it declares, not
here. Restrict write access to workspace administrators.

## Joining %s

Register this repository, sync it, and onboard:

`+"```sh"+`
our manifests add %s <git-url-of-this-repository>
our manifests sync %s
our setup
our ai codex
`+"```"+`

## Publish

Run `+"`our publish`"+` from any directory to create the private remotes for
this manifest and its content repositories, rewrite local mount URLs, and
push everything. Do not push this repository while mounts still reference
local paths.
`, orgName, orgName, orgName, orgID, orgID)
}

func initContentREADME(orgName string) string {
	return fmt.Sprintf(`# %s Handbook

Workspace content for %s: customer identity records, meetings, support
records, fleet records, decisions, projects, policy, and people notes. Record
entries with the our CLI (our meetings add, our support add, our fleet add)
and publish with our sync.
`, orgName, orgName)
}

func initAgentGuidance(orgName string) string {
	return fmt.Sprintf(`# %s Agent Guidance

- Use the generated workspace root as the operating context.
- Prefer the handbook mount for organization-specific facts before asking the operator.
- Keep private notes and operating content in this workspace repository, not in public code.
`, orgName)
}

func initSkillDoc(orgID, orgName string) string {
	return fmt.Sprintf(`---
name: %s-handbook
description: Use the %s handbook for customer records, meetings, support records, fleet records, decisions, policy, people, projects, and workspace-specific operating context.
---

# %s Handbook

Use this skill when work depends on %s-specific context from the Our AI
workspace. Start with the generated root guidance, then inspect the relevant
handbook directories or use the `+"`our meetings`"+`, `+"`our support`"+`, and
`+"`our fleet`"+` commands.
`, orgID, orgName, orgName, orgName)
}

func initSectionREADME(title, body string) string {
	return fmt.Sprintf("# %s\n\n%s\n", title, body)
}

func gitInitCommit(root string) error {
	if err := runGit(root, "init", "--quiet"); err != nil {
		return err
	}
	if err := runGit(root, "add", "."); err != nil {
		return err
	}
	if err := runGit(root, "commit", "--quiet", "-m", "Initial Our AI workspace"); err == nil {
		return nil
	}
	// No usable git identity configured; commit with a neutral fallback.
	return runGit(root, "-c", "user.name=Our AI", "-c", "user.email=our-ai@example.invalid", "commit", "--quiet", "-m", "Initial Our AI workspace")
}

func runGit(root string, args ...string) error {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(out))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
}

func isGitCheckout(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func isBareGitRepo(path string) bool {
	if isGitCheckout(path) {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(path, "objects"))
	return err == nil
}

func gitCmdOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func gitDirtyFiles(dir string) ([]string, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git status: %s", strings.TrimSpace(string(out)))
	}
	var files []string
	// Porcelain lines are "XY path"; do not trim the line first — the
	// two-character status may start with a significant space.
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) > 3 {
			files = append(files, strings.TrimSpace(line[3:]))
		}
	}
	return files, nil
}

type publishStep struct {
	Target  string `json:"target"`
	Repo    string `json:"repo,omitempty"`
	Action  string `json:"action"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
}

type publishResult struct {
	Manifest        string        `json:"manifest"`
	Steps           []publishStep `json:"steps"`
	TeammateCommand string        `json:"teammate_command,omitempty"`
}

func (a app) runPublish(args []string) error {
	var home string
	var manifestName string
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("our publish", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "publish one registered manifest")
	fs.BoolVar(&printOnly, "print", false, "print the planned actions without creating or pushing anything")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, `Usage of our publish:
  our publish [--manifest NAME] [--home DIR] [--print] [--json]

Publishes the organization: creates private remotes for content repositories
and the manifest repository when they have none, rewrites local mount URLs to
the published remotes, commits that manifest change, and pushes everything.
Idempotent; existing remotes are adopted and pushed, never recreated.

Options:`)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("publish does not accept positional arguments")
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	result, err := a.publishOrg(home, doc, printOnly)
	if jsonOut {
		if printErr := printJSON(a.stdout, result); printErr != nil {
			return printErr
		}
		return err
	}
	for _, step := range result.Steps {
		line := fmt.Sprintf("publish\t%s\t%s", step.Target, step.Action)
		if step.URL != "" {
			line += "\t" + step.URL
		}
		if step.Message != "" {
			line += "\t" + step.Message
		}
		fmt.Fprintln(a.stdout, line)
	}
	if result.TeammateCommand != "" {
		fmt.Fprintf(a.stdout, "next\tjoin\t%s\n", result.TeammateCommand)
	}
	return err
}

func (a app) publishOrg(home string, doc registeredDoc, printOnly bool) (publishResult, error) {
	runner := a.publishRunner
	if runner == nil {
		runner = func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		}
	}
	orgID := doc.doc.Organization.ID
	result := publishResult{Manifest: doc.ref.Name}
	newDoc := doc.doc
	rewritten := false
	for i := range newDoc.Mounts {
		mount := newDoc.Mounts[i]
		if !localMountGitURL(mount.GitURL) {
			continue
		}
		sourcePath := mount.GitURL
		if isBareGitRepo(sourcePath) {
			// Already a served remote (e.g. a local bare repository).
			continue
		}
		if !isGitCheckout(sourcePath) {
			result.Steps = append(result.Steps, publishStep{Target: "content:" + mount.ID, Action: "failed", Message: "mount source " + sourcePath + " is not a git repository"})
			return result, fmt.Errorf("mount %q source %s is not a git repository", mount.ID, sourcePath)
		}
		repoName := orgID + "-" + mount.ID
		url, step, err := a.ensurePublishedRepo(sourcePath, repoName, "content:"+mount.ID, printOnly, runner)
		result.Steps = append(result.Steps, step)
		if err != nil {
			return result, err
		}
		if !printOnly && url != "" {
			newDoc.Mounts[i].GitURL = url
			rewritten = true
		}
	}
	if rewritten {
		if err := manifest.SaveDocument(doc.ref.LocalPath, newDoc); err != nil {
			return result, err
		}
		if err := runGit(doc.ref.LocalPath, "add", "--", "manifest.json"); err != nil {
			return result, err
		}
		// Commit only the URL rewrite so unrelated admin edits stay local.
		if err := runGit(doc.ref.LocalPath, "commit", "--quiet", "-m", "Point mounts at published repositories", "--", "manifest.json"); err != nil {
			if identityErr := runGit(doc.ref.LocalPath, "-c", "user.name=Our AI", "-c", "user.email=our-ai@example.invalid", "commit", "--quiet", "-m", "Point mounts at published repositories", "--", "manifest.json"); identityErr != nil {
				return result, identityErr
			}
		}
		result.Steps = append(result.Steps, publishStep{Target: "manifest", Action: "rewrote-mounts"})
	}
	manifestURL, step, err := a.ensurePublishedRepo(doc.ref.LocalPath, orgID+"-manifest", "manifest", printOnly, runner)
	result.Steps = append(result.Steps, step)
	if err != nil {
		return result, err
	}
	if !printOnly && manifestURL != "" {
		if filepath.IsAbs(doc.ref.GitURL) && !manifest.SameRemote(doc.ref.GitURL, manifestURL) {
			if _, err := manifest.Add(home, doc.ref.Name, manifestURL); err != nil {
				return result, err
			}
			if _, err := manifest.SetLocalPath(home, doc.ref.Name, doc.ref.LocalPath); err != nil {
				return result, err
			}
		}
		result.TeammateCommand = "our manifests add " + doc.ref.Name + " " + manifestURL
	}
	return result, nil
}

// ensurePublishedRepo adopts an existing origin remote (verifying GitHub
// repositories are private) or creates a private remote for the checkout,
// then pushes; it never recreates an existing remote.

// ensurePublishedRepo adopts an existing origin remote (verifying GitHub
// repositories are private) or creates a private remote for the checkout,
// then pushes; it never recreates an existing remote.
func (a app) ensurePublishedRepo(path, repoName, target string, printOnly bool, runner manifest.Runner) (string, publishStep, error) {
	origin, err := gitCmdOutput(path, "remote", "get-url", "origin")
	if err == nil && origin != "" {
		if printOnly {
			return origin, publishStep{Target: target, Repo: repoName, Action: "would push", URL: origin}, nil
		}
		if _, ok := githubRepoSlug(origin); ok {
			visibility, visErr := a.githubRepoVisibility(origin)
			if visErr != nil {
				return "", publishStep{Target: target, Action: "failed", URL: origin, Message: "cannot verify repository visibility"}, fmt.Errorf("cannot verify visibility of %s: %v; check gh auth status and repository access", origin, visErr)
			}
			if !strings.EqualFold(visibility, "PRIVATE") {
				return "", publishStep{Target: target, Action: "failed", URL: origin, Message: "repository is not private"}, fmt.Errorf("%s is %s; make it private or point origin at a private repository before publishing", origin, strings.ToLower(visibility))
			}
		}
		if err := runGit(path, "push", "origin", "HEAD"); err != nil {
			return "", publishStep{Target: target, Action: "failed", URL: origin, Message: err.Error()}, err
		}
		return origin, publishStep{Target: target, Repo: repoName, Action: "pushed", URL: origin}, nil
	}
	if printOnly {
		return "", publishStep{Target: target, Repo: repoName, Action: "would create", Message: "gh repo create " + repoName + " --private --source " + path + " --push"}, nil
	}
	out, err := runner("gh", "repo", "create", repoName, "--private", "--source", path, "--remote", "origin", "--push")
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return "", publishStep{Target: target, Repo: repoName, Action: "failed", Message: message}, fmt.Errorf("gh repo create %s: %s", repoName, message)
	}
	origin, err = gitCmdOutput(path, "remote", "get-url", "origin")
	if err != nil || origin == "" {
		return "", publishStep{Target: target, Repo: repoName, Action: "failed", Message: "origin remote missing after gh repo create"}, fmt.Errorf("origin remote missing after gh repo create %s", repoName)
	}
	return origin, publishStep{Target: target, Repo: repoName, Action: "created", URL: origin}, nil
}

func localMountGitURL(gitURL string) bool {
	gitURL = strings.TrimSpace(gitURL)
	if gitURL == "" {
		return false
	}
	if filepath.IsAbs(gitURL) {
		return true
	}
	return strings.HasPrefix(gitURL, "./") || strings.HasPrefix(gitURL, "../") || strings.HasPrefix(gitURL, "~/")
}

func initNextCommands(home, orgID string) []initNextCommand {
	manifestFlag := ""
	if initManifestFlagNeeded(home) {
		manifestFlag = " --manifest " + shellQuote(orgID)
	}
	return []initNextCommand{
		{Action: "setup", Command: "our setup" + manifestFlag},
		{Action: "launch", Command: "our ai" + manifestFlag + " claude"},
		{Action: "launch", Command: "our ai" + manifestFlag + " codex"},
		{Action: "publish", Command: "our publish" + manifestFlag},
	}
}

func initManifestFlagNeeded(home string) bool {
	reg, err := manifest.LoadRegistry(home)
	return err == nil && len(reg.Manifests) > 1
}

func (a app) printInitResult(result initResult) {
	fmt.Fprintf(a.stdout, "manifest-repo\tcreated\t%s\n", result.RepoPath)
	fmt.Fprintf(a.stdout, "content-repo\tcreated\t%s\n", result.ContentPath)
	fmt.Fprintf(a.stdout, "manifest\tregistered\t%s\t%s\n", result.Manifest.Name, result.Manifest.LocalPath)
	for _, item := range result.Sync {
		fmt.Fprintf(a.stdout, "manifest-sync\t%s\t%s\n", item.Status, item.LocalPath)
	}
	for _, next := range result.NextCommands {
		fmt.Fprintf(a.stdout, "next\t%s\t%s\n", next.Action, next.Command)
	}
}

func displayNameFromID(id string) string {
	parts := strings.Split(id, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

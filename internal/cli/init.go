package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
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
	fs := newFlagSet("my init", a.stderr)
	fs.StringVar(&opts.orgName, "name", "", "organization display name")
	fs.StringVar(&opts.repoPath, "path", "", "content repository path")
	fs.StringVar(&opts.umbrellaPath, "umbrella", "", "recommended umbrella path")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.BoolVar(&opts.setup, "setup", false, "run my setup after creating and syncing the manifest")
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
		return fmt.Errorf("usage: my init <org-id>")
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
		return a.runSetup(setupArgs)
	}
	return nil
}

func (a app) printInitUsage() {
	fmt.Fprintln(a.stderr, `Usage of my init:
  my init <org-id> [--name NAME] [--path DIR] [--umbrella DIR] [--home DIR] [--setup] [--json]

Creates two local repositories and registers the organization: a private
manifest repository (the control plane: manifest, catalog, skills) and a
content repository mounted in the umbrella (the workspace handbook). Both are
local-only until my publish creates their remotes.

Examples:
  my init acme --name "Acme"
  my init acme --path ~/acme/handbook

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
	result.NextCommands = initNextCommands(opts.orgID)
	return result, nil
}

// initMountID names the content mount my init declares: the org workspace,
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
				// Local until my publish rewrites it to the hosted remote.
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
		filepath.Join("meetings", "README.md"):  initSectionREADME("Meetings", "Record meeting notes with my meetings add, then publish with my sync --push."),
		filepath.Join("support", "README.md"):   initSectionREADME("Support", "Record anonymized problem-to-solution notes with my support add."),
		filepath.Join("fleet", "README.md"):     initSectionREADME("Fleet", "Track deployed instances or devices with my fleet add and my fleet set."),
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
	return fmt.Sprintf(`# %s My AI Manifest

This private repository is the %s organization manifest: the control plane
that defines mounts, skills, the product/repo catalog, and agent guidance. Day-to-day
workspace content lives in the mounted content repositories it declares, not
here. Restrict write access to workspace administrators.

## Joining %s

Register this repository, sync it, and onboard:

`+"```sh"+`
my manifests add %s <git-url-of-this-repository>
my manifests sync %s
my setup --manifest %s
my ai --manifest %s codex
`+"```"+`

## Publish

Run `+"`my publish --manifest %s`"+` from any directory to create the private
remotes for this manifest and its content repositories, rewrite local mount
URLs, commit reviewed manifest control-plane changes, and push everything. Do
not push this repository while mounts still reference local paths.
`, orgName, orgName, orgName, orgID, orgID, orgID, orgID, orgID)
}

func initContentREADME(orgName string) string {
	return fmt.Sprintf(`# %s Handbook

Workspace content for %s: customer identity records, meetings, support
records, fleet records, decisions, projects, policy, and people notes. Record
entries with my commands (my customers add, my meetings add, my support add,
my fleet add) and publish with my sync --push.
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

Use this skill when work depends on %s-specific context from the My AI
workspace. Start with the generated root guidance, then inspect the relevant
handbook directories or use the `+"`my customers`"+`, `+"`my meetings`"+`,
`+"`my support`"+`, and `+"`my fleet`"+` commands.
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
	if err := runGit(root, "commit", "--quiet", "-m", "Initial My AI workspace"); err == nil {
		return nil
	}
	// No usable git identity configured; commit with a neutral fallback.
	return runGit(root, "-c", "user.name=My AI", "-c", "user.email=my-cli@example.invalid", "commit", "--quiet", "-m", "Initial My AI workspace")
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
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git status: %s", strings.TrimSpace(string(out)))
	}
	var files []string
	parts := strings.Split(string(out), "\x00")
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if len(part) < 4 {
			continue
		}
		status := part[:2]
		path := part[3:]
		if path != "" {
			files = append(files, filepath.ToSlash(path))
		}
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			i++
			// Rename records include the old path as a second NUL-delimited field.
			// Keep both sides so the allowlist can reject cross-boundary renames.
			if i < len(parts) && (status[0] == 'R' || status[1] == 'R') {
				if oldPath := parts[i]; oldPath != "" {
					files = append(files, filepath.ToSlash(oldPath))
				}
			}
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
	fs := newFlagSet("my publish", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "publish one registered manifest")
	fs.BoolVar(&printOnly, "print", false, "print the planned actions without creating or pushing anything")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, `Usage of my publish:
  my publish [--manifest NAME] [--home DIR] [--print] [--json]

Publishes the organization: creates private remotes for content repositories
and the manifest repository when they have none, rewrites local mount URLs to
the published remotes, commits reviewed manifest control-plane changes, and
pushes everything.
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
	if !printOnly && manifest.GovernanceConfigured(doc.doc.Governance) {
		authorized, _, _, err := a.loadAuthorizedAdminManifestCheckout(doc.ref.LocalPath)
		if err != nil {
			return publishResult{Manifest: doc.ref.Name}, err
		}
		doc.doc = authorized
	}
	runner := a.publishRunner
	if runner == nil {
		runner = func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		}
	}
	orgID := doc.doc.Organization.ID
	result := publishResult{Manifest: doc.ref.Name}
	if step, blocked, err := manifestControlPublishStep(doc.ref.LocalPath, printOnly); blocked || err != nil {
		if step.Action != "" {
			result.Steps = append(result.Steps, step)
		}
		return result, err
	}
	newDoc := doc.doc
	rewritten := false
	var plannedControlFiles []string
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
		if url != "" {
			if !printOnly {
				newDoc.Mounts[i].GitURL = url
			} else {
				plannedControlFiles = append(plannedControlFiles, "manifest.json")
			}
			rewritten = true
		}
	}
	if rewritten {
		if printOnly {
			result.Steps = append(result.Steps, publishStep{Target: "manifest", Action: "would rewrite-mounts"})
		} else {
			if err := manifest.SaveDocument(doc.ref.LocalPath, newDoc); err != nil {
				return result, err
			}
			result.Steps = append(result.Steps, publishStep{Target: "manifest", Action: "rewrote-mounts"})
		}
	}
	if step, blocked, err := commitManifestControlChanges(doc.ref.LocalPath, printOnly, plannedControlFiles); blocked || err != nil {
		if step.Action != "" {
			result.Steps = append(result.Steps, step)
		}
		return result, err
	} else if step.Action != "" {
		result.Steps = append(result.Steps, step)
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
		result.TeammateCommand = "my manifests add " + doc.ref.Name + " " + manifestURL
	}
	return result, nil
}

func manifestControlPublishStep(root string, printOnly bool) (publishStep, bool, error) {
	_, blocked, err := manifestControlDirtyFiles(root)
	if err != nil {
		return publishStep{Target: "manifest", Action: "failed", Message: err.Error()}, true, err
	}
	if len(blocked) == 0 {
		return publishStep{}, false, nil
	}
	step := publishStep{
		Target:  "manifest",
		Action:  "held back",
		Message: "dirty files outside manifest control paths: " + strings.Join(blocked, ", "),
	}
	if printOnly {
		return step, true, nil
	}
	return step, true, fmt.Errorf("manifest checkout has dirty files outside manifest control paths: %s", strings.Join(blocked, ", "))
}

func commitManifestControlChanges(root string, printOnly bool, plannedAllowed []string) (publishStep, bool, error) {
	allowed, blocked, err := manifestControlDirtyFiles(root)
	if err != nil {
		return publishStep{Target: "manifest", Action: "failed", Message: err.Error()}, true, err
	}
	allowed = uniqueStrings(append(append([]string{}, plannedAllowed...), allowed...))
	if len(blocked) != 0 {
		step := publishStep{
			Target:  "manifest",
			Action:  "held back",
			Message: "dirty files outside manifest control paths: " + strings.Join(blocked, ", "),
		}
		if printOnly {
			return step, true, nil
		}
		return step, true, fmt.Errorf("manifest checkout has dirty files outside manifest control paths: %s", strings.Join(blocked, ", "))
	}
	if len(allowed) == 0 {
		return publishStep{}, false, nil
	}
	step := publishStep{
		Target:  "manifest",
		Action:  "committed",
		Message: strings.Join(allowed, ", "),
	}
	if printOnly {
		step.Action = "would commit"
		return step, false, nil
	}
	stagePaths := manifestControlStagePaths(allowed)
	args := append([]string{"add", "-A", "--"}, stagePaths...)
	if err := runGit(root, args...); err != nil {
		return publishStep{Target: "manifest", Action: "failed", Message: err.Error()}, true, err
	}
	commitArgs := append([]string{"commit", "--quiet", "-m", "Publish manifest control-plane changes", "--"}, stagePaths...)
	if err := runGit(root, commitArgs...); err != nil {
		fallbackArgs := append([]string{"-c", "user.name=My AI", "-c", "user.email=my-cli@example.invalid"}, commitArgs...)
		if identityErr := runGit(root, fallbackArgs...); identityErr != nil {
			return publishStep{Target: "manifest", Action: "failed", Message: identityErr.Error()}, true, identityErr
		}
	}
	return step, false, nil
}

func manifestControlDirtyFiles(root string) ([]string, []string, error) {
	files, err := gitDirtyFiles(root)
	if err != nil {
		return nil, nil, err
	}
	var allowed []string
	var blocked []string
	for _, file := range files {
		file = filepath.ToSlash(strings.TrimPrefix(file, "./"))
		if pathsWithinContent([]string{file}, manifestControlPaths()) {
			allowed = append(allowed, file)
		} else {
			blocked = append(blocked, file)
		}
	}
	return allowed, blocked, nil
}

func manifestControlStagePaths(files []string) []string {
	var out []string
	for _, file := range files {
		file = filepath.ToSlash(strings.TrimPrefix(file, "./"))
		for _, root := range manifestControlPaths() {
			if file == root || strings.HasPrefix(file, root+"/") {
				out = append(out, root)
				break
			}
		}
	}
	return uniqueStrings(out)
}

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

func initNextCommands(orgID string) []initNextCommand {
	manifestFlag := " --manifest " + shellQuote(orgID)
	return []initNextCommand{
		{Action: "setup", Command: "my setup" + manifestFlag},
		{Action: "launch", Command: "my ai" + manifestFlag + " claude"},
		{Action: "launch", Command: "my ai" + manifestFlag + " codex"},
		{Action: "publish", Command: "my publish" + manifestFlag},
	}
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

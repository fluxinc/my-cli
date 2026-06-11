// Package manifest manages organization manifests used by the our CLI.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/ghauth"
)

const (
	registryVersion = 1
	appDir          = "our"
	manifestFile    = "manifest.json"
)

// Registry records configured organization manifests on this machine.
type Registry struct {
	Version   int   `json:"version"`
	Manifests []Ref `json:"manifests"`
}

// Ref points at one configured organization manifest repository.
type Ref struct {
	Name      string `json:"name"`
	GitURL    string `json:"git_url"`
	LocalPath string `json:"local_path"`
}

// SyncResult describes one manifest sync action.
type SyncResult struct {
	Name      string `json:"name"`
	GitURL    string `json:"git_url"`
	LocalPath string `json:"local_path"`
	Status    string `json:"status"`
	Changed   bool   `json:"changed,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ValidationResult is a machine-readable manifest validation report.
type ValidationResult struct {
	Path     string   `json:"path"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Document is the organization manifest.json schema consumed by the CLI.
type Document struct {
	ManifestVersion           int           `json:"manifest_version"`
	Organization              Organization  `json:"organization"`
	AllowedExternalNamespaces []string      `json:"allowed_external_namespaces,omitempty"`
	Umbrella                  Umbrella      `json:"umbrella,omitzero"`
	AgentGuidance             AgentGuidance `json:"agent_guidance,omitzero"`
	Sync                      SyncPolicy    `json:"sync,omitzero"`
	Skills                    []Skill       `json:"skills,omitempty"`
	Mounts                    []Mount       `json:"mounts,omitempty"`
	Workspaces                []Workspace   `json:"workspaces,omitempty"`
	Tools                     []Tool        `json:"tools,omitempty"`
}

// Organization identifies the organization owning this manifest.
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Skill describes one skill source in a manifest.
type Skill struct {
	ID           string   `json:"id"`
	InstallSlug  string   `json:"install_slug"`
	Path         string   `json:"path,omitempty"`
	Source       Source   `json:"source,omitzero"`
	Capabilities []string `json:"capabilities,omitempty"`
	Requires     []string `json:"requires,omitempty"`
}

// Source describes non-manifest-repo skill sources such as tool-provided skills.
type Source struct {
	Type string `json:"type,omitempty"`
	Tool string `json:"tool,omitempty"`
}

// Umbrella describes the local organization workspace envelope.
type Umbrella struct {
	RecommendedPath string `json:"recommended_path,omitempty"`
}

// AgentGuidance describes manifest-owned additions to generated workspace
// AGENTS.md files.
type AgentGuidance struct {
	Paths []string `json:"paths,omitempty"`
}

// SyncPolicy controls workspace-wide sync behavior.
type SyncPolicy struct {
	PublishPolicy string `json:"publish_policy,omitempty"`
}

// Mount describes one content source that can be cloned into an umbrella.
type Mount struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	GitURL       string   `json:"git_url"`
	Mode         string   `json:"mode"`
	IncludePaths []string `json:"include_paths,omitempty"`
}

// Product describes one catalog product that can be opted into an umbrella.
type Product struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	GitURL        string   `json:"git_url"`
	Description   string   `json:"description"`
	Purpose       string   `json:"purpose,omitempty"`
	RelatedSkills []string `json:"related_skills,omitempty"`
}

// Customer describes one canonical customer identity.
type Customer struct {
	ID              string   `json:"id"`
	Name            string   `json:"name,omitempty"`
	Domain          string   `json:"domain,omitempty"`
	DomainConfirmed bool     `json:"domain_confirmed,omitempty"`
	Aliases         []string `json:"aliases,omitempty"`
	Partners        []string `json:"partners,omitempty"`
}

// Workspace describes one local knowledge workspace in a manifest.
type Workspace struct {
	ID        string `json:"id"`
	GitURL    string `json:"git_url"`
	LocalPath string `json:"local_path"`
}

// Tool describes an optional or required external tool.
type Tool struct {
	ID           string       `json:"id"`
	Mode         string       `json:"mode"`
	Purpose      string       `json:"purpose"`
	Install      ToolInstall  `json:"install,omitzero"`
	SkillInstall SkillInstall `json:"skill_install,omitzero"`
}

// ToolInstall describes operator-facing install hints for a tool.
type ToolInstall struct {
	Commands []string `json:"commands,omitempty"`
	DocsURL  string   `json:"docs_url,omitempty"`
}

// SkillInstall describes how a tool can materialize its own agent skill.
type SkillInstall struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// Runner executes external commands. Tests can replace it.
type Runner func(name string, args ...string) ([]byte, error)

// LoadRegistry reads the local manifest registry. Missing registry means empty.
func LoadRegistry(home string) (Registry, error) {
	path, err := RegistryPath(home)
	if err != nil {
		return Registry{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Registry{Version: registryVersion}, nil
	}
	if err != nil {
		return Registry{}, err
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("read manifest registry: %w", err)
	}
	if reg.Version == 0 {
		reg.Version = registryVersion
	}
	return reg, nil
}

// SaveRegistry writes the local manifest registry.
func SaveRegistry(home string, reg Registry) error {
	path, err := RegistryPath(home)
	if err != nil {
		return err
	}
	if reg.Version == 0 {
		reg.Version = registryVersion
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Add registers or updates one organization manifest source.
func Add(home, name, gitURL string) (Ref, error) {
	if !portableID(name) {
		return Ref{}, fmt.Errorf("manifest name %q must be lowercase kebab-case", name)
	}
	if strings.TrimSpace(gitURL) == "" {
		return Ref{}, fmt.Errorf("manifest git URL is required")
	}
	reg, err := LoadRegistry(home)
	if err != nil {
		return Ref{}, err
	}
	homeDir, err := resolveHome(home)
	if err != nil {
		return Ref{}, err
	}
	ref := Ref{
		Name:      name,
		GitURL:    gitURL,
		LocalPath: filepath.Join(cacheRoot(homeDir), "manifests", name),
	}
	for i, existing := range reg.Manifests {
		if existing.Name == name {
			if SameRemote(existing.GitURL, gitURL) && strings.TrimSpace(existing.LocalPath) != "" {
				// Re-adding the same source must not clobber a re-pointed
				// checkout (e.g. a self-hosted manifest living in its umbrella).
				ref.LocalPath = existing.LocalPath
			}
			reg.Manifests[i] = ref
			return ref, SaveRegistry(home, reg)
		}
	}
	reg.Manifests = append(reg.Manifests, ref)
	return ref, SaveRegistry(home, reg)
}

// DefaultCachePath returns the registry's default checkout location for a
// manifest name, whether or not it is registered.
func DefaultCachePath(home, name string) (string, error) {
	homeDir, err := resolveHome(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheRoot(homeDir), "manifests", name), nil
}

// SetLocalPath re-points a registered manifest at an existing local checkout.
func SetLocalPath(home, name, localPath string) (Ref, error) {
	if strings.TrimSpace(localPath) == "" {
		return Ref{}, fmt.Errorf("manifest local path is required")
	}
	reg, err := LoadRegistry(home)
	if err != nil {
		return Ref{}, err
	}
	for i, existing := range reg.Manifests {
		if existing.Name == name {
			reg.Manifests[i].LocalPath = localPath
			return reg.Manifests[i], SaveRegistry(home, reg)
		}
	}
	return Ref{}, fmt.Errorf("manifest %q is not registered; run our manifests add %s <git-url>", name, name)
}

// NormalizeRemote canonicalizes a git remote URL for equality checks. It
// mirrors the syncer's remote-key normalization so the two layers agree on
// which checkouts are the same repository.
func NormalizeRemote(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimSuffix(value, "/")
	return value
}

// SameRemote reports whether two git URLs point at the same remote.
func SameRemote(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return NormalizeRemote(a) == NormalizeRemote(b)
}

// Find returns a configured manifest by name.
func Find(home, name string) (Ref, bool, error) {
	reg, err := LoadRegistry(home)
	if err != nil {
		return Ref{}, false, err
	}
	for _, ref := range reg.Manifests {
		if ref.Name == name {
			return ref, true, nil
		}
	}
	return Ref{}, false, nil
}

// Sync clones or fast-forwards configured manifest repositories.
func Sync(home string, names []string, all bool, dryRun bool, runner Runner) ([]SyncResult, error) {
	reg, err := LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	if runner == nil {
		runner = execCommand
	}
	refs, err := selectedRefs(reg, names, all)
	if err != nil {
		return nil, err
	}
	results := make([]SyncResult, 0, len(refs))
	for _, ref := range refs {
		results = append(results, syncOne(ref, dryRun, runner))
	}
	return results, nil
}

// ValidateFile validates an org manifest JSON file or a directory containing it.
func ValidateFile(path string) ValidationResult {
	result := ValidationResult{Path: path}
	doc, resolved, err := LoadDocument(path)
	result.Path = resolved
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	validateOrgManifest(doc, &result)
	validateProductCatalog(filepath.Dir(resolved), doc, &result)
	validateCustomerCatalog(filepath.Dir(resolved), &result)
	return result
}

// ValidateDocument validates an in-memory manifest document against the same
// schema rules as ValidateFile. Catalog checks run when root is not empty.
func ValidateDocument(root string, doc Document) ValidationResult {
	result := ValidationResult{Path: root}
	validateOrgManifest(doc, &result)
	if root != "" {
		validateProductCatalog(root, doc, &result)
		validateCustomerCatalog(root, &result)
	}
	return result
}

// LoadDocument reads a manifest JSON file or directory containing manifest.json.
func LoadDocument(path string) (Document, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Document{}, path, err
	}
	if info.IsDir() {
		path = filepath.Join(path, manifestFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, path, err
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return Document{}, path, fmt.Errorf("invalid JSON: %w", err)
	}
	return doc, path, nil
}

// SaveDocument writes manifest.json using the canonical JSON formatting.
func SaveDocument(path string, doc Document) error {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		path = filepath.Join(path, manifestFile)
	} else if errors.Is(err, os.ErrNotExist) && filepath.Base(path) != manifestFile {
		path = filepath.Join(path, manifestFile)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadCatalog reads catalog/products.json from a registered manifest repo.
func LoadCatalog(home, manifestName string) ([]Product, error) {
	ref, err := singleRef(home, manifestName)
	if err != nil {
		return nil, err
	}
	path := ProductCatalogPath(ref)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Product{}, nil
	}
	if err != nil {
		return nil, err
	}
	var products []Product
	if err := json.Unmarshal(data, &products); err != nil {
		return nil, fmt.Errorf("read product catalog %s: invalid JSON%s: %w", path, jsonErrorOffset(err), err)
	}
	doc, _, err := LoadDocument(ref.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("load manifest for product catalog %s: %w", path, err)
	}
	if err := validateProducts(path, products, manifestSkillIDs(doc)); err != nil {
		return nil, err
	}
	return products, nil
}

// LoadCustomers reads catalog/customers.json from selected registered manifests.
func LoadCustomers(home, manifestName string) ([]Customer, error) {
	refs, err := selectedCatalogRefs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var out []Customer
	for _, ref := range refs {
		customers, err := loadCustomersFromRef(ref)
		if err != nil {
			return nil, err
		}
		out = append(out, customers...)
	}
	return out, nil
}

// FindCustomer resolves a canonical customer by id, alias, or domain.
func FindCustomer(home, manifestName, value string) (Customer, bool, error) {
	value = normalizeCustomerLookup(value)
	if value == "" {
		return Customer{}, false, nil
	}
	customers, err := LoadCustomers(home, manifestName)
	if err != nil {
		return Customer{}, false, err
	}
	for _, customer := range customers {
		if normalizeCustomerLookup(customer.ID) == value || normalizeCustomerLookup(customer.Domain) == value {
			return customer, true, nil
		}
		for _, alias := range customer.Aliases {
			if normalizeCustomerLookup(alias) == value {
				return customer, true, nil
			}
		}
	}
	return Customer{}, false, nil
}

// FindProduct returns one product catalog entry by id.
func FindProduct(home, manifestName, id string) (Product, bool, error) {
	products, err := LoadCatalog(home, manifestName)
	if err != nil {
		return Product{}, false, err
	}
	for _, product := range products {
		if product.ID == id {
			return product, true, nil
		}
	}
	return Product{}, false, nil
}

// ManifestPath returns the expected manifest.json path for a registered ref.
func ManifestPath(ref Ref) string {
	return filepath.Join(ref.LocalPath, manifestFile)
}

// ProductCatalogPath returns the expected product catalog path for a registered ref.
func ProductCatalogPath(ref Ref) string {
	return filepath.Join(ref.LocalPath, "catalog", "products.json")
}

// CustomerCatalogPath returns the expected catalog/customers.json path for a registered ref.
func CustomerCatalogPath(ref Ref) string {
	return filepath.Join(ref.LocalPath, "catalog", "customers.json")
}

// EffectiveMounts returns native mounts plus legacy workspaces projected into
// mount shape for transition.
func EffectiveMounts(doc Document) []Mount {
	out := make([]Mount, 0, len(doc.Mounts)+len(doc.Workspaces))
	seen := map[string]bool{}
	for _, mount := range doc.Mounts {
		out = append(out, mount)
		seen[mount.ID] = true
	}
	for _, workspace := range doc.Workspaces {
		if seen[workspace.ID] {
			continue
		}
		out = append(out, Mount{
			ID:     workspace.ID,
			Kind:   legacyWorkspaceKind(workspace.ID),
			GitURL: workspace.GitURL,
			Mode:   "required",
		})
	}
	return out
}

func legacyWorkspaceKind(id string) string {
	if id == "handbook" {
		return "handbook"
	}
	return "repo"
}

// RegistryPath returns the path to the local manifest registry file.
func RegistryPath(home string) (string, error) {
	resolved, err := resolveHome(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, ".config", appDir, "manifests.json"), nil
}

func selectedRefs(reg Registry, names []string, all bool) ([]Ref, error) {
	if all {
		if len(names) != 0 {
			return nil, fmt.Errorf("--all cannot be combined with explicit manifest names")
		}
		return reg.Manifests, nil
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("select a manifest name or pass --all")
	}
	byName := make(map[string]Ref, len(reg.Manifests))
	for _, ref := range reg.Manifests {
		byName[ref.Name] = ref
	}
	out := make([]Ref, 0, len(names))
	for _, name := range names {
		ref, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("manifest %q is not registered", name)
		}
		out = append(out, ref)
	}
	return out, nil
}

func singleRef(home, manifestName string) (Ref, error) {
	if manifestName != "" {
		ref, ok, err := Find(home, manifestName)
		if err != nil {
			return Ref{}, err
		}
		if !ok {
			return Ref{}, fmt.Errorf("manifest %q is not registered", manifestName)
		}
		return ref, nil
	}
	reg, err := LoadRegistry(home)
	if err != nil {
		return Ref{}, err
	}
	if len(reg.Manifests) == 0 {
		return Ref{}, fmt.Errorf("no registered manifests")
	}
	if len(reg.Manifests) != 1 {
		return Ref{}, fmt.Errorf("multiple manifests registered; pass --manifest")
	}
	return reg.Manifests[0], nil
}

func selectedCatalogRefs(home, manifestName string) ([]Ref, error) {
	if manifestName != "" {
		ref, ok, err := Find(home, manifestName)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("manifest %q is not registered", manifestName)
		}
		return []Ref{ref}, nil
	}
	reg, err := LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	return reg.Manifests, nil
}

func loadCustomersFromRef(ref Ref) ([]Customer, error) {
	path := CustomerCatalogPath(ref)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Customer{}, nil
	}
	if err != nil {
		return nil, err
	}
	var customers []Customer
	if err := json.Unmarshal(data, &customers); err != nil {
		return nil, fmt.Errorf("read customer catalog %s: invalid JSON%s: %w", path, jsonErrorOffset(err), err)
	}
	if err := validateCustomers(path, customers); err != nil {
		return nil, err
	}
	return customers, nil
}

func syncOne(ref Ref, dryRun bool, runner Runner) SyncResult {
	res := SyncResult{Name: ref.Name, GitURL: ref.GitURL, LocalPath: ref.LocalPath}
	if _, err := os.Stat(filepath.Join(ref.LocalPath, ".git")); err == nil {
		if dryRun {
			res.Status = "dry-run"
			res.Message = fmt.Sprintf("would run git -C %s pull --ff-only", ref.LocalPath)
			return res
		}
		if _, err := runner("git", "-C", ref.LocalPath, "remote", "get-url", "origin"); err != nil {
			res.Status = "local-only"
			res.Message = "no origin remote configured; nothing to pull until the repository is published"
			return res
		}
		if err := ghauth.CheckGitURL(ref.GitURL, ghauth.Runner(runner)); err != nil {
			res.Status = "failed"
			res.Error = err.Error()
			return res
		}
		before, beforeErr := gitHead(ref.LocalPath, runner)
		out, err := runner("git", "-C", ref.LocalPath, "pull", "--ff-only")
		if err != nil {
			res.Status = "failed"
			res.Error = strings.TrimSpace(string(out))
			if res.Error == "" {
				res.Error = err.Error()
			}
			return res
		}
		res.Status = "synced"
		after, afterErr := gitHead(ref.LocalPath, runner)
		if beforeErr != nil || afterErr != nil || before != after {
			res.Changed = true
		}
		res.Message = strings.TrimSpace(string(out))
		return res
	}
	if dryRun {
		res.Status = "dry-run"
		res.Message = fmt.Sprintf("would run git clone %s %s", ref.GitURL, ref.LocalPath)
		return res
	}
	if err := ghauth.CheckGitURL(ref.GitURL, ghauth.Runner(runner)); err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	if err := os.MkdirAll(filepath.Dir(ref.LocalPath), 0o755); err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	out, err := runner("git", "clone", ref.GitURL, ref.LocalPath)
	if err != nil {
		res.Status = "failed"
		res.Error = strings.TrimSpace(string(out))
		if res.Error == "" {
			res.Error = err.Error()
		}
		return res
	}
	res.Status = "synced"
	res.Changed = true
	res.Message = strings.TrimSpace(string(out))
	return res
}

func gitHead(path string, runner Runner) (string, error) {
	out, err := runner("git", "-C", path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func validateOrgManifest(doc Document, result *ValidationResult) {
	if doc.ManifestVersion <= 0 {
		result.Errors = append(result.Errors, "manifest_version must be a positive integer")
	}
	if !portableID(doc.Organization.ID) {
		result.Errors = append(result.Errors, "organization.id must be lowercase kebab-case")
	}
	allowed := map[string]bool{doc.Organization.ID: true}
	for _, ns := range doc.AllowedExternalNamespaces {
		if !portableID(ns) {
			result.Errors = append(result.Errors, fmt.Sprintf("allowed_external_namespaces contains invalid namespace %q", ns))
			continue
		}
		allowed[ns] = true
	}
	mountIDs := map[string]bool{}
	for _, m := range EffectiveMounts(doc) {
		if portableID(m.ID) {
			mountIDs[m.ID] = true
		}
	}
	tools := map[string]Tool{}
	for _, t := range doc.Tools {
		if portableID(t.ID) {
			tools[t.ID] = t
		}
	}
	for _, s := range doc.Skills {
		validateSkill(s, allowed, mountIDs, tools, result)
	}
	validateUmbrella(doc.Umbrella, result)
	validateAgentGuidance(doc.AgentGuidance, result)
	validateSyncPolicy(doc.Sync, result)
	for _, m := range doc.Mounts {
		validateMount(m, result)
	}
	for _, w := range doc.Workspaces {
		validateWorkspace(w, result)
	}
	for _, t := range doc.Tools {
		validateTool(t, result)
	}
}

func validateSyncPolicy(policy SyncPolicy, result *ValidationResult) {
	if policy.PublishPolicy == "" {
		return
	}
	if !validPublishPolicy(policy.PublishPolicy) {
		result.Errors = append(result.Errors, fmt.Sprintf("sync.publish_policy %q is unsupported", policy.PublishPolicy))
	}
}

func validPublishPolicy(value string) bool {
	switch value {
	case "auto", "never", "pr":
		return true
	default:
		return false
	}
}

func validateProductCatalog(root string, doc Document, result *ValidationResult) {
	path := filepath.Join(root, "catalog", "products.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return
	}
	var products []Product
	if err := json.Unmarshal(data, &products); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("read product catalog %s: invalid JSON%s: %v", path, jsonErrorOffset(err), err))
		return
	}
	if err := validateProducts(path, products, manifestSkillIDs(doc)); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func validateCustomerCatalog(root string, result *ValidationResult) {
	path := filepath.Join(root, "catalog", "customers.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return
	}
	var customers []Customer
	if err := json.Unmarshal(data, &customers); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("read customer catalog %s: invalid JSON%s: %v", path, jsonErrorOffset(err), err))
		return
	}
	if err := validateCustomers(path, customers); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
}

func manifestSkillIDs(doc Document) map[string]bool {
	out := make(map[string]bool, len(doc.Skills))
	for _, skill := range doc.Skills {
		out[skill.ID] = true
	}
	return out
}

func validateUmbrella(u Umbrella, result *ValidationResult) {
	if u.RecommendedPath == "" {
		return
	}
	if strings.TrimSpace(u.RecommendedPath) == "" {
		result.Errors = append(result.Errors, "umbrella.recommended_path must not be blank")
	}
}

func validateAgentGuidance(g AgentGuidance, result *ValidationResult) {
	for _, path := range g.Paths {
		if !portableIncludePath(path) {
			result.Errors = append(result.Errors, fmt.Sprintf("agent_guidance paths entry %q must be a relative path that stays inside the manifest repo", path))
		}
	}
}

func validateMount(m Mount, result *ValidationResult) {
	if !portableID(m.ID) {
		result.Errors = append(result.Errors, fmt.Sprintf("mount id %q must be lowercase kebab-case", m.ID))
	}
	if !validMountKind(m.Kind) {
		result.Errors = append(result.Errors, fmt.Sprintf("mount %q kind %q is unsupported", m.ID, m.Kind))
	}
	if !validMountMode(m.Mode) {
		result.Errors = append(result.Errors, fmt.Sprintf("mount %q mode %q is unsupported", m.ID, m.Mode))
	}
	gitURL := strings.TrimSpace(m.GitURL)
	if gitURL == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("mount %q git_url is required", m.ID))
	} else if gitURL == "." {
		result.Errors = append(result.Errors, fmt.Sprintf("mount %q git_url must point at a separate content repository; \".\" self-mounts are no longer supported", m.ID))
	} else if strings.HasPrefix(gitURL, "git@") {
		result.Warnings = append(result.Warnings, fmt.Sprintf("mount %q uses SSH URL; gh auth login does not configure SSH keys", m.ID))
	}
	for _, includePath := range m.IncludePaths {
		if !portableIncludePath(includePath) {
			result.Errors = append(result.Errors, fmt.Sprintf("mount %q include_paths entry %q must be a relative path that stays inside the repo", m.ID, includePath))
		}
	}
}

func validateSkill(s Skill, allowed, mountIDs map[string]bool, tools map[string]Tool, result *ValidationResult) {
	if s.ID == "" {
		result.Errors = append(result.Errors, "skill id is required")
	} else {
		parts := strings.SplitN(s.ID, ":", 2)
		if len(parts) != 2 || !portableID(parts[0]) || !portableID(parts[1]) {
			result.Errors = append(result.Errors, fmt.Sprintf("skill id %q must be namespace:name with lowercase kebab-case parts", s.ID))
		} else if !allowed[parts[0]] {
			result.Errors = append(result.Errors, fmt.Sprintf("skill id %q uses namespace %q not declared by organization.id or allowed_external_namespaces", s.ID, parts[0]))
		}
	}
	if !portableID(s.InstallSlug) {
		result.Errors = append(result.Errors, fmt.Sprintf("skill %q install_slug must be lowercase kebab-case", s.ID))
	}
	sourceType := s.Source.Type
	switch sourceType {
	case "", "static":
		if s.Source.Tool != "" {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q source.tool is only valid when source.type is %q", s.ID, "tool"))
		}
		if s.Path == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q path is required", s.ID))
		} else if filepath.IsAbs(s.Path) || pathEscapes(s.Path) {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q path must be relative and stay inside the manifest repo", s.ID))
		}
	case "tool":
		if !portableID(s.Source.Tool) {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q source.tool must be a lowercase kebab-case tool id", s.ID))
		} else if tool, ok := tools[s.Source.Tool]; !ok {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q source.tool references unknown tool %q", s.ID, s.Source.Tool))
		} else if strings.TrimSpace(tool.SkillInstall.Command) == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q source.tool %q does not declare skill_install.command", s.ID, s.Source.Tool))
		}
		if s.Path != "" && (filepath.IsAbs(s.Path) || pathEscapes(s.Path)) {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q path must be relative and stay inside the materialized skills root", s.ID))
		}
	default:
		result.Errors = append(result.Errors, fmt.Sprintf("skill %q source.type %q is unsupported", s.ID, sourceType))
	}
	for _, req := range s.Requires {
		validateSkillRequirement(s.ID, req, mountIDs, tools, result)
	}
}

func validateSkillRequirement(skillID, req string, mountIDs map[string]bool, tools map[string]Tool, result *ValidationResult) {
	parts := strings.SplitN(req, ":", 2)
	if len(parts) != 2 || !portableID(parts[0]) || !portableID(parts[1]) {
		result.Errors = append(result.Errors, fmt.Sprintf("skill %q requires entry %q must be type:id with lowercase kebab-case parts", skillID, req))
		return
	}
	switch parts[0] {
	case "workspace":
		if !mountIDs[parts[1]] {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q requires unknown workspace or mount %q", skillID, parts[1]))
		}
	case "tool":
		if _, ok := tools[parts[1]]; !ok {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q requires unknown tool %q", skillID, parts[1]))
		}
	default:
		result.Errors = append(result.Errors, fmt.Sprintf("skill %q requires unsupported dependency type %q", skillID, parts[0]))
	}
}

func validateTool(t Tool, result *ValidationResult) {
	if !portableID(t.ID) {
		result.Errors = append(result.Errors, fmt.Sprintf("tool id %q must be lowercase kebab-case", t.ID))
	}
	if t.Mode != "" && t.Mode != "required" && t.Mode != "optional" {
		result.Errors = append(result.Errors, fmt.Sprintf("tool %q mode must be required or optional", t.ID))
	}
	if len(t.SkillInstall.Args) != 0 && strings.TrimSpace(t.SkillInstall.Command) == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("tool %q skill_install.command is required when skill_install.args are provided", t.ID))
	}
	if t.SkillInstall.Command != "" && strings.TrimSpace(t.SkillInstall.Command) == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("tool %q skill_install.command must not be blank", t.ID))
	}
}

func validMountKind(kind string) bool {
	switch kind {
	case "handbook", "meetings", "support", "fleet", "policy", "docs", "repo":
		return true
	default:
		return false
	}
}

func validMountMode(mode string) bool {
	switch mode {
	case "required", "default", "optional":
		return true
	default:
		return false
	}
}

func portableIncludePath(value string) bool {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return false
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	clean := pathpkg.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	return true
}

func validateWorkspace(w Workspace, result *ValidationResult) {
	if !portableID(w.ID) {
		result.Errors = append(result.Errors, fmt.Sprintf("workspace id %q must be lowercase kebab-case", w.ID))
	}
	if strings.TrimSpace(w.GitURL) == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("workspace %q git_url is required", w.ID))
	} else if strings.HasPrefix(w.GitURL, "git@") {
		result.Warnings = append(result.Warnings, fmt.Sprintf("workspace %q uses SSH URL; gh auth login does not configure SSH keys", w.ID))
	}
	if strings.TrimSpace(w.LocalPath) == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("workspace %q local_path is required", w.ID))
	}
}

func validateProducts(path string, products []Product, knownSkillIDs map[string]bool) error {
	seen := map[string]bool{}
	for _, product := range products {
		if !portableID(product.ID) {
			return fmt.Errorf("product catalog %s: product id %q must be lowercase kebab-case", path, product.ID)
		}
		if seen[product.ID] {
			return fmt.Errorf("product catalog %s: duplicate product id %q", path, product.ID)
		}
		seen[product.ID] = true
		if strings.TrimSpace(product.GitURL) == "" {
			return fmt.Errorf("product catalog %s: product %q git_url is required", path, product.ID)
		}
		for _, skillID := range product.RelatedSkills {
			if !portableNamespacedID(skillID) {
				return fmt.Errorf("product catalog %s: product %q related skill %q must be namespace:name with lowercase kebab-case parts", path, product.ID, skillID)
			}
			if knownSkillIDs != nil && !knownSkillIDs[skillID] {
				return fmt.Errorf("product catalog %s: product %q related skill %q is not declared by manifest", path, product.ID, skillID)
			}
		}
	}
	return nil
}

func validateCustomers(path string, customers []Customer) error {
	seen := map[string]bool{}
	seenAliases := map[string]string{}
	for _, customer := range customers {
		if !customerID(customer.ID) {
			return fmt.Errorf("customer catalog %s: customer id %q must be lowercase FQDN-style or kebab-case", path, customer.ID)
		}
		if seen[customer.ID] {
			return fmt.Errorf("customer catalog %s: duplicate customer id %q", path, customer.ID)
		}
		seen[customer.ID] = true
		for _, value := range append([]string{customer.Domain}, customer.Aliases...) {
			normalized := normalizeCustomerLookup(value)
			if normalized == "" {
				continue
			}
			if existing := seenAliases[normalized]; existing != "" && existing != customer.ID {
				return fmt.Errorf("customer catalog %s: customer alias/domain %q is used by both %q and %q", path, value, existing, customer.ID)
			}
			seenAliases[normalized] = customer.ID
		}
	}
	return nil
}

// ValidateCustomers reports whether a customer catalog is internally consistent.
func ValidateCustomers(path string, customers []Customer) error {
	return validateCustomers(path, customers)
}

// ValidCustomerID reports whether value is an accepted canonical customer id.
func ValidCustomerID(value string) bool {
	return customerID(value)
}

func portableNamespacedID(value string) bool {
	parts := strings.SplitN(value, ":", 2)
	return len(parts) == 2 && portableID(parts[0]) && portableID(parts[1])
}

func jsonErrorOffset(err error) string {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Sprintf(" at offset %d", syntaxErr.Offset)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Sprintf(" at offset %d", typeErr.Offset)
	}
	return ""
}

func pathEscapes(path string) bool {
	clean := filepath.Clean(path)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator))
}

func portableID(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func customerID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	lastPunct := true
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			lastPunct = false
			continue
		}
		if r == '-' || r == '.' {
			if lastPunct {
				return false
			}
			lastPunct = true
			continue
		}
		return false
	}
	return !lastPunct
}

func normalizeCustomerLookup(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func cacheRoot(home string) string {
	return filepath.Join(home, ".local", "share", appDir)
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}

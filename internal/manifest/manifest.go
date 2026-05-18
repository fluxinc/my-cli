// Package manifest manages organization manifests used by the flux CLI.
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

	"github.com/fluxinc/flux/internal/ghauth"
)

const (
	registryVersion = 1
	appDir          = "flux"
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
	AllowedExternalNamespaces []string      `json:"allowed_external_namespaces"`
	Umbrella                  Umbrella      `json:"umbrella"`
	AgentGuidance             AgentGuidance `json:"agent_guidance"`
	Skills                    []Skill       `json:"skills"`
	Mounts                    []Mount       `json:"mounts"`
	Workspaces                []Workspace   `json:"workspaces"`
	Tools                     []Tool        `json:"tools"`
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
	Path         string   `json:"path"`
	Source       Source   `json:"source"`
	Capabilities []string `json:"capabilities"`
	Requires     []string `json:"requires"`
}

// Source describes non-manifest-repo skill sources such as tool-provided skills.
type Source struct {
	Type string `json:"type"`
	Tool string `json:"tool,omitempty"`
}

// Umbrella describes the local organization workspace envelope.
type Umbrella struct {
	RecommendedPath string `json:"recommended_path"`
}

// AgentGuidance describes manifest-owned additions to generated workspace
// AGENTS.md files.
type AgentGuidance struct {
	Paths []string `json:"paths"`
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
	Install      ToolInstall  `json:"install"`
	SkillInstall SkillInstall `json:"skill_install"`
}

// ToolInstall describes operator-facing install hints for a tool.
type ToolInstall struct {
	Commands []string `json:"commands"`
	DocsURL  string   `json:"docs_url"`
}

// SkillInstall describes how a tool can materialize its own agent skill.
type SkillInstall struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
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
			reg.Manifests[i] = ref
			return ref, SaveRegistry(home, reg)
		}
	}
	reg.Manifests = append(reg.Manifests, ref)
	return ref, SaveRegistry(home, reg)
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

func syncOne(ref Ref, dryRun bool, runner Runner) SyncResult {
	res := SyncResult{Name: ref.Name, GitURL: ref.GitURL, LocalPath: ref.LocalPath}
	if _, err := os.Stat(filepath.Join(ref.LocalPath, ".git")); err == nil {
		if dryRun {
			res.Status = "dry-run"
			res.Message = fmt.Sprintf("would run git -C %s pull --ff-only", ref.LocalPath)
			return res
		}
		if err := ghauth.CheckGitURL(ref.GitURL, ghauth.Runner(runner)); err != nil {
			res.Status = "failed"
			res.Error = err.Error()
			return res
		}
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
	res.Message = strings.TrimSpace(string(out))
	return res
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
	if strings.TrimSpace(m.GitURL) == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("mount %q git_url is required", m.ID))
	} else if strings.HasPrefix(m.GitURL, "git@") {
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
	if len(t.SkillInstall.Args) != 0 && strings.TrimSpace(t.SkillInstall.Command) == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("tool %q skill_install.command is required when skill_install.args are provided", t.ID))
	}
	if t.SkillInstall.Command != "" && strings.TrimSpace(t.SkillInstall.Command) == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("tool %q skill_install.command must not be blank", t.ID))
	}
}

func validMountKind(kind string) bool {
	switch kind {
	case "handbook", "meetings", "policy", "docs", "repo":
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

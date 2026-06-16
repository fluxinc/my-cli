// Package launchplan projects a My AI manifest into the deterministic
// Clawdapus-facing launch artifact used by `my compile`.
package launchplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

const (
	compileVersion = 1
	target         = "clawdapus"
)

var baselineFleetWorkContract = []string{
	"Before substantive work on a deployed instance, run `my fleet get <id|identifier>` so you start from the registry record and see related support history.",
	"Continue an existing relevant support record when one is listed, or create a new dated anonymized record with `my support add` for a distinct incident or work session.",
	"Put the fleet record id and every useful fleet identifier on the support record with repeated `--identifier` flags, plus customer, product, and area frontmatter when known.",
	"Treat support records as the incident/work log. Fleet records hold registry state; use `my fleet set` only for meaningful state transitions.",
	"Publish the resulting content with `my sync --push`.",
}

// Options controls launch projection compilation.
type Options struct {
	Role string
}

// Projection is the deterministic JSON artifact printed by `my compile`.
type Projection struct {
	CompileVersion int                    `json:"compile_version"`
	Target         string                 `json:"target"`
	Organization   manifest.Organization  `json:"organization"`
	Role           string                 `json:"role,omitempty"`
	Contract       []ContractBlock        `json:"contract"`
	Guidance       []GuidanceRef          `json:"guidance"`
	Mounts         []Mount                `json:"mounts"`
	DataBindings   map[string]DataBinding `json:"data_bindings"`
	Services       []Service              `json:"services"`
	Skills         []Skill                `json:"skills"`
	Tools          []Tool                 `json:"tools"`
}

// ContractBlock preserves source and include-mode metadata for later Clawdapus
// emission.
type ContractBlock struct {
	Source string   `json:"source"`
	Mode   string   `json:"mode"`
	Rules  []string `json:"rules"`
}

// GuidanceRef describes a manifest guidance fragment and its later include mode.
type GuidanceRef struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Mode    string `json:"mode"`
	Path    string `json:"path"`
	Surface string `json:"surface,omitempty"`
}

// Mount projects one manifest mount. Kind remains the manifest's semantic kind.
type Mount struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Mode         string   `json:"mode"`
	GitURL       string   `json:"git_url"`
	IncludePaths []string `json:"include_paths"`
}

// DataBinding projects one in-scope data binding.
type DataBinding struct {
	Surface string `json:"surface"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
}

// Service projects one non-secret service declaration.
type Service struct {
	ID          string                     `json:"id"`
	Kind        string                     `json:"kind"`
	Purpose     string                     `json:"purpose"`
	DescribeRef string                     `json:"describe_ref,omitempty"`
	AuthRef     string                     `json:"auth_ref"`
	Connection  manifest.ServiceConnection `json:"connection,omitzero"`
}

// Skill projects materialization-relevant skill metadata.
type Skill struct {
	ID           string          `json:"id"`
	InstallSlug  string          `json:"install_slug"`
	Path         string          `json:"path,omitempty"`
	Source       manifest.Source `json:"source,omitzero"`
	Capabilities []string        `json:"capabilities,omitempty"`
	Requires     []string        `json:"requires,omitempty"`
}

// Tool projects materialization-relevant external tool metadata.
type Tool struct {
	ID           string                `json:"id"`
	Mode         string                `json:"mode"`
	Purpose      string                `json:"purpose"`
	Install      manifest.ToolInstall  `json:"install,omitzero"`
	SkillInstall manifest.SkillInstall `json:"skill_install,omitzero"`
}

// Compile creates a deterministic launch projection. The caller is expected to
// pass a manifest that already passed manifest validation; Compile still checks
// role closure and Mode-B-only constraints such as local mount URLs.
func Compile(doc manifest.Document, opts Options) (Projection, error) {
	role, hasRole, err := selectRole(doc, opts.Role)
	if err != nil {
		return Projection{}, err
	}

	allMounts := mountByID(doc.Mounts)
	allServices := serviceByID(doc.Services)
	allSkills := skillByID(doc.Skills)
	allTools := toolByID(doc.Tools)

	visibleMounts, err := selectMounts(doc.Mounts, allMounts, role.Mounts, hasRole)
	if err != nil {
		return Projection{}, err
	}
	visibleServices, err := selectServices(doc.Services, allServices, role.Services, hasRole)
	if err != nil {
		return Projection{}, err
	}
	visibleSkills, err := selectSkills(doc.Skills, allSkills, role.Skills, hasRole)
	if err != nil {
		return Projection{}, err
	}
	visibleTools, err := selectTools(doc.Tools, allTools, role.Tools, hasRole)
	if err != nil {
		return Projection{}, err
	}

	mountSet := idSetMounts(visibleMounts)
	serviceSet := idSetServices(visibleServices)
	toolSet := idSetTools(visibleTools)
	if err := validateSkillClosure(visibleSkills, mountSet, serviceSet, toolSet); err != nil {
		return Projection{}, err
	}

	mounts, err := projectMounts(visibleMounts)
	if err != nil {
		return Projection{}, err
	}
	bindings, err := projectDataBindings(doc.DataBindings, allMounts, allServices, mountSet, serviceSet)
	if err != nil {
		return Projection{}, err
	}

	projection := Projection{
		CompileVersion: compileVersion,
		Target:         target,
		Organization:   doc.Organization,
		Role:           opts.Role,
		Contract:       projectContract(doc.Contract),
		Guidance:       projectGuidance(doc, role, hasRole, bindings),
		Mounts:         mounts,
		DataBindings:   bindings,
		Services:       projectServices(visibleServices),
		Skills:         projectSkills(visibleSkills),
		Tools:          projectTools(visibleTools),
	}
	return projection, nil
}

// Marshal returns the stable JSON encoding used by the CLI and golden tests.
func Marshal(projection Projection) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(projection); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func selectRole(doc manifest.Document, roleID string) (manifest.Role, bool, error) {
	if roleID == "" {
		if len(doc.Roles) != 0 {
			return manifest.Role{}, false, fmt.Errorf("role is required when manifest declares roles; run my roles list")
		}
		return manifest.Role{}, false, nil
	}
	for _, role := range doc.Roles {
		if role.ID == roleID {
			return role, true, nil
		}
	}
	return manifest.Role{}, false, fmt.Errorf("role %q not found; run my roles list", roleID)
}

func projectContract(rules []string) []ContractBlock {
	blocks := []ContractBlock{{
		Source: "baseline:fleet-work-contract",
		Mode:   "enforce",
		Rules:  append([]string(nil), baselineFleetWorkContract...),
	}}
	if len(rules) != 0 {
		blocks = append(blocks, ContractBlock{
			Source: "manifest:contract",
			Mode:   "enforce",
			Rules:  append([]string(nil), rules...),
		})
	}
	return blocks
}

func projectGuidance(doc manifest.Document, role manifest.Role, hasRole bool, bindings map[string]DataBinding) []GuidanceRef {
	var out []GuidanceRef
	for _, p := range doc.AgentGuidance.Paths {
		out = append(out, GuidanceRef{
			ID:     pathID("agent-guidance", p),
			Source: "agent_guidance",
			Mode:   "guide",
			Path:   p,
		})
	}
	if hasRole {
		for _, p := range role.GuidancePaths {
			out = append(out, GuidanceRef{
				ID:     pathID("role-"+role.ID, p),
				Source: "role:" + role.ID,
				Mode:   "guide",
				Path:   p,
			})
		}
	}
	dataTypes := make([]string, 0, len(doc.DataBindings))
	for dataType := range doc.DataBindings {
		dataTypes = append(dataTypes, dataType)
	}
	sort.Strings(dataTypes)
	for _, dataType := range dataTypes {
		binding := doc.DataBindings[dataType]
		if len(binding.Guidance) == 0 {
			continue
		}
		if _, ok := bindings[dataType]; !ok {
			continue
		}
		for _, p := range binding.Guidance {
			out = append(out, GuidanceRef{
				ID:      pathID("domain-"+dataType, p),
				Source:  "domain:" + dataType,
				Mode:    "reference",
				Path:    p,
				Surface: binding.Surface,
			})
		}
	}
	if out == nil {
		return []GuidanceRef{}
	}
	return out
}

func projectMounts(mounts []manifest.Mount) ([]Mount, error) {
	out := make([]Mount, 0, len(mounts))
	for _, mount := range mounts {
		if localGitURL(mount.GitURL) {
			return nil, fmt.Errorf("mount %q git_url %q is local; publish the mount before compiling a contained launch projection", mount.ID, mount.GitURL)
		}
		out = append(out, Mount{
			ID:           mount.ID,
			Kind:         mount.Kind,
			Mode:         mount.Mode,
			GitURL:       mount.GitURL,
			IncludePaths: append([]string(nil), mount.IncludePaths...),
		})
	}
	return out, nil
}

func projectDataBindings(bindings map[string]manifest.DataBinding, allMounts map[string]manifest.Mount, allServices map[string]manifest.Service, visibleMounts, visibleServices map[string]bool) (map[string]DataBinding, error) {
	out := map[string]DataBinding{}
	dataTypes := make([]string, 0, len(bindings))
	for dataType := range bindings {
		dataTypes = append(dataTypes, dataType)
	}
	sort.Strings(dataTypes)
	for _, dataType := range dataTypes {
		binding := bindings[dataType]
		kind, id, ok := manifest.ParseSurfaceRef(binding.Surface)
		if !ok {
			return nil, fmt.Errorf("data binding %s has invalid surface %q", dataType, binding.Surface)
		}
		switch kind {
		case "mount":
			if _, ok := allMounts[id]; !ok {
				return nil, fmt.Errorf("data binding %s references unknown mount %q", dataType, id)
			}
			if !visibleMounts[id] {
				continue
			}
		case "service":
			if _, ok := allServices[id]; !ok {
				return nil, fmt.Errorf("data binding %s references unknown service %q", dataType, id)
			}
			if !visibleServices[id] {
				continue
			}
		}
		out[dataType] = DataBinding{
			Surface: binding.Surface,
			Kind:    kind,
			Ref:     id,
		}
	}
	return out, nil
}

func projectServices(services []manifest.Service) []Service {
	out := make([]Service, 0, len(services))
	for _, service := range services {
		out = append(out, Service{
			ID:          service.ID,
			Kind:        service.Kind,
			Purpose:     service.Purpose,
			DescribeRef: service.DescribeRef,
			AuthRef:     service.AuthRef,
			Connection:  service.Connection,
		})
	}
	return out
}

func projectSkills(skills []manifest.Skill) []Skill {
	out := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		out = append(out, Skill{
			ID:           skill.ID,
			InstallSlug:  skill.InstallSlug,
			Path:         skill.Path,
			Source:       skill.Source,
			Capabilities: append([]string(nil), skill.Capabilities...),
			Requires:     append([]string(nil), skill.Requires...),
		})
	}
	return out
}

func projectTools(tools []manifest.Tool) []Tool {
	out := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, Tool{
			ID:           tool.ID,
			Mode:         tool.Mode,
			Purpose:      tool.Purpose,
			Install:      tool.Install,
			SkillInstall: tool.SkillInstall,
		})
	}
	return out
}

func validateSkillClosure(skills []manifest.Skill, mounts, services, tools map[string]bool) error {
	for _, skill := range skills {
		if skill.Source.Type == "tool" && !tools[skill.Source.Tool] {
			return fmt.Errorf("skill %q source tool %q outside selected role scope", skill.ID, skill.Source.Tool)
		}
		for _, req := range skill.Requires {
			kind, id, ok := strings.Cut(req, ":")
			if !ok {
				return fmt.Errorf("skill %q requires invalid dependency %q", skill.ID, req)
			}
			switch kind {
			case "workspace":
				if !mounts[id] {
					return fmt.Errorf("skill %q requires workspace %q outside selected role scope", skill.ID, id)
				}
			case "service":
				if !services[id] {
					return fmt.Errorf("skill %q requires service %q outside selected role scope", skill.ID, id)
				}
			case "tool":
				if !tools[id] {
					return fmt.Errorf("skill %q requires tool %q outside selected role scope", skill.ID, id)
				}
			default:
				return fmt.Errorf("skill %q requires unsupported dependency type %q", skill.ID, kind)
			}
		}
	}
	return nil
}

func selectMounts(all []manifest.Mount, byID map[string]manifest.Mount, selected []string, hasRole bool) ([]manifest.Mount, error) {
	if !hasRole {
		return append([]manifest.Mount(nil), all...), nil
	}
	selectedSet, err := selectedIDs("mount", selected, byID)
	if err != nil {
		return nil, err
	}
	out := make([]manifest.Mount, 0, len(selected))
	for _, item := range all {
		if selectedSet[item.ID] {
			out = append(out, item)
		}
	}
	return out, nil
}

func selectServices(all []manifest.Service, byID map[string]manifest.Service, selected []string, hasRole bool) ([]manifest.Service, error) {
	if !hasRole {
		return append([]manifest.Service(nil), all...), nil
	}
	selectedSet, err := selectedIDs("service", selected, byID)
	if err != nil {
		return nil, err
	}
	out := make([]manifest.Service, 0, len(selected))
	for _, item := range all {
		if selectedSet[item.ID] {
			out = append(out, item)
		}
	}
	return out, nil
}

func selectSkills(all []manifest.Skill, byID map[string]manifest.Skill, selected []string, hasRole bool) ([]manifest.Skill, error) {
	if !hasRole {
		return append([]manifest.Skill(nil), all...), nil
	}
	selectedSet, err := selectedIDs("skill", selected, byID)
	if err != nil {
		return nil, err
	}
	out := make([]manifest.Skill, 0, len(selected))
	for _, item := range all {
		if selectedSet[item.ID] {
			out = append(out, item)
		}
	}
	return out, nil
}

func selectTools(all []manifest.Tool, byID map[string]manifest.Tool, selected []string, hasRole bool) ([]manifest.Tool, error) {
	if !hasRole {
		return append([]manifest.Tool(nil), all...), nil
	}
	selectedSet, err := selectedIDs("tool", selected, byID)
	if err != nil {
		return nil, err
	}
	out := make([]manifest.Tool, 0, len(selected))
	for _, item := range all {
		if selectedSet[item.ID] {
			out = append(out, item)
		}
	}
	return out, nil
}

type identifiable interface {
	manifest.Mount | manifest.Service | manifest.Skill | manifest.Tool
}

func selectedIDs[T identifiable](kind string, selected []string, byID map[string]T) (map[string]bool, error) {
	out := make(map[string]bool, len(selected))
	for _, id := range selected {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("role selects unknown %s %q", kind, id)
		}
		out[id] = true
	}
	return out, nil
}

func mountByID(items []manifest.Mount) map[string]manifest.Mount {
	out := make(map[string]manifest.Mount, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func serviceByID(items []manifest.Service) map[string]manifest.Service {
	out := make(map[string]manifest.Service, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func skillByID(items []manifest.Skill) map[string]manifest.Skill {
	out := make(map[string]manifest.Skill, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func toolByID(items []manifest.Tool) map[string]manifest.Tool {
	out := make(map[string]manifest.Tool, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func idSetMounts(items []manifest.Mount) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = true
	}
	return out
}

func idSetServices(items []manifest.Service) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = true
	}
	return out
}

func idSetTools(items []manifest.Tool) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = true
	}
	return out
}

func localGitURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "file://") || strings.HasPrefix(value, "~") {
		return true
	}
	if value == "." || value == ".." || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return true
	}
	if filepath.IsAbs(value) || windowsAbsPath(value) || strings.HasPrefix(value, `\\`) {
		return true
	}
	return !strings.Contains(value, "://") && !strings.Contains(value, ":")
}

func windowsAbsPath(value string) bool {
	if len(value) < 3 {
		return false
	}
	drive := value[0]
	if !((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) {
		return false
	}
	return value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func pathID(prefix, p string) string {
	base := strings.TrimSuffix(p, path.Ext(p))
	base = strings.Trim(base, "/")
	if base == "" {
		return prefix
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ".", "-", "_", "-")
	return prefix + "-" + replacer.Replace(base)
}

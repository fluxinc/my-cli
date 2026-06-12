package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/fleet"
	"github.com/fluxinc/our-ai/internal/meetings"
	"github.com/fluxinc/our-ai/internal/support"
	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/worksession"
)

func TestMeetingJSONErrorWithoutUmbrella(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "meetings", "search", "SampleCo", "--home", home, "--json"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stdout.String(), `"error": "no_umbrella"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMeetingsAddMarksCreatedRecordIntentToAdd(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"our", "meetings", "add", "sampleco-followup",
		"--manifest", "acme",
		"--workspace", "handbook",
		"--home", home,
		"--date", "2026-06-12",
	}); err != nil {
		t.Fatal(err)
	}

	status := strings.TrimRight(gitCLIOutput(t, workspaceRoot, "status", "--porcelain", "--", "meetings/2026-06-12-sampleco-followup.md"), "\n")
	if !strings.HasPrefix(status, " A ") {
		t.Fatalf("git status = %q, want intent-to-add status", status)
	}
}

func TestMeetingsAddFromSessionWritesSessionMount(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--slug", "meeting", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("parse session JSON: %v\nstdout: %s", err, stdout.String())
	}
	t.Chdir(session.Mounts[0].WorktreePath)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "meetings", "add", "session-note",
		"--home", home,
		"--date", "2026-06-11",
	}); err != nil {
		t.Fatalf("meetings add: %v\nstderr: %s", err, stderr.String())
	}

	rel := filepath.Join("meetings", "2026-06-11-session-note.md")
	sessionRecord := filepath.Join(session.Mounts[0].WorktreePath, rel)
	if got := strings.TrimSpace(stdout.String()); got != sessionRecord {
		t.Fatalf("stdout = %q, want session record %q", stdout.String(), sessionRecord)
	}
	if _, err := os.Stat(sessionRecord); err != nil {
		t.Fatalf("session record missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, rel)); !os.IsNotExist(err) {
		t.Fatalf("base record was written or stat failed: %v", err)
	}
	sessionStatus := strings.TrimRight(gitCLIOutput(t, session.Mounts[0].WorktreePath, "status", "--porcelain", "--", rel), "\n")
	if !strings.HasPrefix(sessionStatus, " A ") {
		t.Fatalf("session git status = %q, want intent-to-add", sessionStatus)
	}
	baseStatus := strings.TrimSpace(gitCLIOutput(t, workspaceRoot, "status", "--porcelain", "--", rel))
	if baseStatus != "" {
		t.Fatalf("base git status = %q, want clean", baseStatus)
	}
	if _, err := worksession.Load(umbrellaRoot, session.ID); err != nil {
		t.Fatalf("session registry missing: %v", err)
	}
}

func TestRecordAdoptMarksContentFileIntentToAdd(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	path := filepath.Join(workspaceRoot, "meetings", "manual-note.md")
	writeCLITestFile(t, path, "manual\n")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"our", "record", "adopt", path,
		"--manifest", "acme",
		"--workspace", "handbook",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	status := strings.TrimRight(gitCLIOutput(t, workspaceRoot, "status", "--porcelain", "--", "meetings/manual-note.md"), "\n")
	if !strings.HasPrefix(status, " A ") {
		t.Fatalf("git status = %q, want intent-to-add status", status)
	}
	if !strings.Contains(stdout.String(), path) {
		t.Fatalf("stdout = %q, want adopted path", stdout.String())
	}
}

func TestMeetingsAddWorksInLocalOnlyWorkspace(t *testing.T) {
	// A founder's freshly initialized org is local-only (no origin remotes);
	// recording must work before anything is published.
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if err := a.run([]string{"our", "setup", "--home", home, "claude"}); err != nil {
		t.Fatalf("setup: %v\nstderr: %s", err, stderr.String())
	}
	stdout.Reset()
	if err := a.run([]string{
		"our", "meetings", "add", "kickoff",
		"--home", home,
		"--date", "2026-06-12",
	}); err != nil {
		t.Fatalf("meetings add in local-only workspace: %v", err)
	}
	record := filepath.Join(home, "acme", "workspace", "meetings", "2026-06-12-kickoff.md")
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("record missing: %v", err)
	}
}

func TestMeetingsCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	if err := os.MkdirAll(filepath.Join(manifestCache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "meetings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "meetings", "2026-03-12-sampleco-implementation.md"), []byte(`---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
customer: sampleco
product: sample-product
status: finalized
---

Promised onboarding review and data cleanup.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "list", "--manifest", "acme", "--home", home, "--customer", "sampleco"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 8 {
		t.Fatalf("meetings list fields = %#v, want 8 fixed columns", fields)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "search", "data cleanup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Promised onboarding review") {
		t.Fatalf("meetings search stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "get", "2026-03-12-sampleco-implementation", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "data cleanup") {
		t.Fatalf("meetings get stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "add", "sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-05-13", "--customer", "sampleco", "--attendees", "Alex Example", "--partner", "integratorco", "--source-id", "spark-123", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-05-13-sampleco-followup") || !strings.Contains(stdout.String(), "## Promises") || !strings.Contains(stdout.String(), `source_id: spark-123`) {
		t.Fatalf("meetings add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "add", "2026-05-28-sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--attendees", "Heather (PMH, mammo tech)", "--partner", "Siemens, Healthineers", "--print"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"2026-05-28-sampleco-followup",
		`date: 2026-05-28`,
		`  - "Heather (PMH, mammo tech)"`,
		`  - "Siemens, Healthineers"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("meetings add stdout = %q, missing %q", out, want)
		}
	}
}

func TestMeetingsUseConfiguredUmbrella(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "default"
    }
  ]
}`)
	root := filepath.Join(home, "acme")
	if _, state, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
		t.Fatal(err)
	} else {
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        "handbook",
			Kind:      "handbook",
			SourceRef: "manifest:acme:handbook",
			Status:    "synced",
		})
		if err := umbrella.SaveState(root, state); err != nil {
			t.Fatal(err)
		}
	}
	writeCLITestFile(t, filepath.Join(root, "handbook", "meetings", "2026-03-12-sampleco-implementation.md"), `---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
customer: sampleco.example.com
---

Data cleanup follow-up.
`)
	writeCLITestFile(t, filepath.Join(root, "handbook", "customers", "registry.md"), `# Customer Registry

## Registry - confirmed FQDN

| Canonical ID | Name | Partner(s) | Notes |
|---|---|---|---|
| `+"`sampleco.example.com`"+` | SampleCo | IntegratorCo | Merged `+"`sampleco`"+`. |
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "customers", "list", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sampleco.example.com") || !strings.Contains(stdout.String(), "sampleco") {
		t.Fatalf("customers list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "list", "--home", home, "--customer", "sampleco"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "search", "sampleco cleanup", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings search stdout = %q", stdout.String())
	}
}

func TestMeetingsSearchUsesQMDOrderWhenAvailable(t *testing.T) {
	root := meetings.Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCLITestFile(t, filepath.Join(root.Path, "meetings", "2026-01-01-alpha.md"), `---
id: 2026-01-01-alpha
date: 2026-01-01
title: Alpha
---

Data cleanup.
`)
	writeCLITestFile(t, filepath.Join(root.Path, "meetings", "2026-02-01-beta.md"), `---
id: 2026-02-01-beta
date: 2026-02-01
title: Beta
---

Data cleanup.
`)
	old := qmdMeetingSearch
	qmdMeetingSearch = func([]meetings.Root, string, meetings.Filter) ([]meetings.Meeting, bool) {
		return []meetings.Meeting{{
			Manifest:  "acme",
			Workspace: "handbook",
			ID:        "2026-01-01-alpha",
			Path:      filepath.Join(root.Path, "meetings", "2026-01-01-alpha.md"),
			Date:      "2026-01-01",
			Title:     "Alpha",
			Snippet:   "qmd snippet",
		}}, true
	}
	defer func() { qmdMeetingSearch = old }()

	found, err := defaultMeetingSearch([]meetings.Root{root}, "data cleanup", meetings.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "2026-01-01-alpha" || found[0].Snippet != "qmd snippet" {
		t.Fatalf("found = %#v", found)
	}
}

func TestSupportCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	if err := os.MkdirAll(filepath.Join(manifestCache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "support"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "support", "2026-06-10-routing-timeout.md"), `---
id: 2026-06-10-routing-timeout
date: 2026-06-10
title: "Routing timeout"
customer: sampleco
identifiers: [ws-12, so-100045]
claimed_by: alex
observed_by: [bo]
approved_by: casey
product: sample-product
area: routing
status: resolved
tags: [timeout, delivery]
feature_candidate: true
source: support
---

The delivery failed with a clear timeout.
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "list", "--manifest", "acme", "--home", home, "--customer", "sampleco", "--identifier", "ws-12", "--claimed-by", "alex", "--area", "routing", "--tag", "timeout", "--feature-candidate"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-06-10-routing-timeout") ||
		!strings.Contains(stdout.String(), "ws-12,so-100045") ||
		!strings.Contains(stdout.String(), "alex") {
		t.Fatalf("support list stdout = %q", stdout.String())
	}
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 13 {
		t.Fatalf("support list fields = %#v, want 13 fixed columns", fields)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "search", "clear timeout", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "clear timeout") {
		t.Fatalf("support search stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "get", "2026-06-10-routing-timeout", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "feature_candidate: true") {
		t.Fatalf("support get stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "add", "sampleco-timeout", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-06-11", "--customer", "sampleco", "--identifier", "ws-12", "--identifier", "fl-400-123401", "--claimed-by", "alex", "--observed-by", "bo", "--product", "sample-product", "--area", "routing", "--tag", "timeout", "--feature-candidate", "--print"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"2026-06-11-sampleco-timeout", "## Diagnosis", "feature_candidate: true", `  - "timeout"`, `  - "fl-400-123401"`, "claimed_by: alex", `  - "bo"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("support add stdout = %q, missing %q", out, want)
		}
	}
}

func TestSupportSearchUsesQMDOrderWhenAvailable(t *testing.T) {
	root := support.Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCLITestFile(t, filepath.Join(root.Path, "support", "2026-01-01-alpha.md"), `---
id: 2026-01-01-alpha
date: 2026-01-01
title: Alpha
---

Timeout delivery.
`)
	writeCLITestFile(t, filepath.Join(root.Path, "support", "2026-02-01-beta.md"), `---
id: 2026-02-01-beta
date: 2026-02-01
title: Beta
---

Timeout delivery.
`)
	old := qmdSupportSearch
	qmdSupportSearch = func([]support.Root, string, support.Filter) ([]support.Record, bool) {
		return []support.Record{{
			Manifest:  "acme",
			Workspace: "handbook",
			ID:        "2026-01-01-alpha",
			Path:      filepath.Join(root.Path, "support", "2026-01-01-alpha.md"),
			Date:      "2026-01-01",
			Title:     "Alpha",
			Snippet:   "qmd snippet",
		}}, true
	}
	defer func() { qmdSupportSearch = old }()

	found, err := defaultSupportSearch([]support.Root{root}, "timeout delivery", support.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "2026-01-01-alpha" || found[0].Snippet != "qmd snippet" {
		t.Fatalf("found = %#v", found)
	}
}

func TestFleetCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	if err := os.MkdirAll(manifestCache, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "fleet", "acme-box-1.md"), `---
id: acme-box-1
customer: sampleco.example.com
partner: integratorco
status: live
device: Sample Scanner X
serial: SN-0001
identifiers:
  - "SO 100045"
  - "FL 400-123401"
config_repo: acme/sample-configs
config_branch: partner/site-1
deployed_site: Springfield
ship_to: Centerville
assigned: alex
source: fleet
---

# acme-box-1

Routing hub for the sample site.
`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "fleet", "acme-box-3.md"), `---
id: acme-box-3
customer: sampleco.example.com
status: staged
identifiers:
  - "SO 300001"
source: fleet
---

# acme-box-3

Staged routing hub.
`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "support", "2026-06-10-routing-timeout.md"), `---
id: 2026-06-10-routing-timeout
date: 2026-06-10
title: "Routing timeout"
identifiers: [FL 400-123401]
status: resolved
source: support
---

The delivery failed with a clear timeout.
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "list", "--manifest", "acme", "--home", home, "--status", "live", "--customer", "sampleco.example.com", "--partner", "integratorco", "--identifier", "SO 100045", "--branch", "partner/site-1", "--where", "assigned=alex"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme-box-1") ||
		!strings.Contains(stdout.String(), "SO 100045,FL 400-123401") {
		t.Fatalf("fleet list stdout = %q", stdout.String())
	}
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 10 {
		t.Fatalf("fleet list fields = %#v, want 10 fixed columns", fields)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "list", "--manifest", "acme", "--home", home, "--where", "assigned=bo"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("fleet list with unmatched where = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "get", "SO 100045", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Routing hub for the sample site.") {
		t.Fatalf("fleet get stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "# Related support records") ||
		!strings.Contains(stdout.String(), "2026-06-10-routing-timeout") {
		t.Fatalf("fleet get related support missing: %q", stdout.String())
	}
	for _, want := range []string{
		"# Support record next step",
		"Continue a relevant support record above",
		"our support add '<slug>' --customer sampleco.example.com --identifier acme-box-1 --identifier 'SO 100045' --identifier 'FL 400-123401'",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("fleet get stdout = %q, missing %q", stdout.String(), want)
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "get", "acme-box-3", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"No related support records were found",
		"our support add '<slug>' --customer sampleco.example.com --identifier acme-box-3 --identifier 'SO 300001'",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("fleet get without related support stdout = %q, missing %q", stdout.String(), want)
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "search", "routing hub", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme-box-1") {
		t.Fatalf("fleet search stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "add", "ACME-BOX-2", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--customer", "sampleco.example.com", "--device", "Sample Scanner Y", "--identifier", "SO 200031", "--config-branch", "partner/site-2", "--ship-to", "Centerville", "--print"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"id: acme-box-2", "status: new", `  - "SO 200031"`, "config_branch: partner/site-2", "deployed_site:\n", "source: fleet", "## Notes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fleet add stdout = %q, missing %q", out, want)
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "set", "acme-box-1", "status=mourn", "deployed_site=Lakeside", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out = stdout.String()
	for _, want := range []string{"updated ", `status: "live" -> "mourn"`, "our sync --message", "Update fleet acme-box-1:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fleet set stdout = %q, missing %q", out, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(workspaceRoot, "fleet", "acme-box-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status: mourn") || !strings.Contains(string(data), `  - "SO 100045"`) {
		t.Fatalf("fleet set file = %q", data)
	}

	stderr.Reset()
	stdout.Reset()
	if err := a.run([]string{"our", "support", "add", "another-timeout", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--identifier", "FL 400-123401", "--identifier", "zz-unknown", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), `"zz-unknown" is not in the fleet registry`) {
		t.Fatalf("support add stderr = %q, want unknown identifier note", stderr.String())
	}
	if strings.Contains(stderr.String(), `"FL 400-123401" is not`) {
		t.Fatalf("support add stderr flagged a known identifier: %q", stderr.String())
	}
}

func TestFleetSearchUsesQMDOrderWhenAvailable(t *testing.T) {
	root := fleet.Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCLITestFile(t, filepath.Join(root.Path, "fleet", "box-a.md"), `---
id: box-a
status: live
---

Timeout delivery.
`)
	writeCLITestFile(t, filepath.Join(root.Path, "fleet", "box-b.md"), `---
id: box-b
status: live
---

Timeout delivery.
`)
	old := qmdFleetSearch
	qmdFleetSearch = func([]fleet.Root, string, fleet.Filter) ([]fleet.Record, bool) {
		return []fleet.Record{{
			Manifest:  "acme",
			Workspace: "handbook",
			ID:        "box-b",
			Path:      filepath.Join(root.Path, "fleet", "box-b.md"),
			Status:    "live",
			Snippet:   "qmd snippet",
		}}, true
	}
	defer func() { qmdFleetSearch = old }()

	found, err := defaultFleetSearch([]fleet.Root{root}, "timeout delivery", fleet.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "box-b" || found[0].Snippet != "qmd snippet" {
		t.Fatalf("found = %#v", found)
	}
}

func TestCustomersListAndMeetingCustomerAlias(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "customers.json"), `[
  {
    "id": "sampleco.example.com",
    "name": "SampleCo",
    "domain": "sampleco.example.com",
    "domain_confirmed": true,
    "aliases": ["sampleco", "sc"],
    "partners": ["integratorco"]
  }
]`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "meetings", "2026-03-12-sampleco-implementation.md"), `---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
customer: sampleco.example.com
---

Alias filter match.
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "customers", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sampleco.example.com") || !strings.Contains(stdout.String(), "sampleco,sc") {
		t.Fatalf("customers list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "list", "--manifest", "acme", "--home", home, "--customer", "sc"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "add", "sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-05-13", "--customer", "sc", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "customer: sampleco.example.com") {
		t.Fatalf("meetings add stdout = %q", stdout.String())
	}
}

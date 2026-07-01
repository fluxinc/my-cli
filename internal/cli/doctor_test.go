package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func TestDoctorReportsGuidanceDrift(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(umbrellaRoot, "AGENTS.md"), "<!-- my:generated workspace-guidance v1 -->\n\nstale\n")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "doctor", "--umbrella", umbrellaRoot, "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "umbrella\tguidance\tstale") ||
		!strings.Contains(stdout.String(), "run my setup") {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
}

func TestDoctorReportsFreshnessNoFetch(t *testing.T) {
	home, _, _, _ := setupCLITrackedManifest(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home, "--no-fetch"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "freshness\tmanifest:acme\tok") ||
		!strings.Contains(out, "up to date (as of last fetch)") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorReportsLocalMountURLRequiresPublish(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home, "--no-fetch"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"manifest\tacme:mount:workspace\tlocal-only", "mount git_url is local-only", "run my publish --manifest acme"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor stdout = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "manifest\tacme\tlocal-only") {
		t.Fatalf("doctor local-mount report should be a mount item, got %q", out)
	}
}

func TestDoctorReportsRemoteFreshnessUnknown(t *testing.T) {
	home, _, manifestCache, _ := setupCLITrackedManifest(t)
	runCLIGit(t, manifestCache, "remote", "set-url", "origin", filepath.Join(home, "missing.git"))
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "freshness\tmanifest:acme\tunknown") ||
		!strings.Contains(out, "behind=unknown (remote unreachable)") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorSkipsSkillDriftForMissingHarnessDirs(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived\tskill:claude-code:acme-handbook\tabsent") {
		t.Fatalf("doctor stdout = %q, want missing harness dir skipped", out)
	}
	if !strings.Contains(out, "derived\tskills\tok") ||
		!strings.Contains(out, "no legacy user-global org skills detected") {
		t.Fatalf("doctor stdout = %q, want no legacy global org skill drift", out)
	}
}

func TestDoctorDoesNotReportAbsentGlobalOrgSkillAsDrift(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived\tskill:claude-code:acme-handbook\tabsent") {
		t.Fatalf("doctor stdout = %q, absent global org skill should not be drift", out)
	}
	if !strings.Contains(out, "derived\tskills\tok") ||
		!strings.Contains(out, "no legacy user-global org skills detected") {
		t.Fatalf("doctor stdout = %q, want launch-scoped org skill ok", out)
	}
}

func TestDoctorReportsLegacyGlobalOrgSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	writeCLIManagedSkill(t, filepath.Join(home, ".claude", "skills", "acme-handbook"), "acme:handbook")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "derived\tskill:claude-code:acme-handbook\tlegacy-global") ||
		!strings.Contains(out, "remove legacy user-global org skill") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorFixPreservesManualGlobalOrgSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "skills", "install", "claude-code", "--manifest", "acme", "--home", home, "--skill", "acme:handbook"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".claude", "skills", "acme-handbook")
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("manual skill missing before doctor: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "doctor", "--fix", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("doctor removed manual global org skill: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Contains(stdout.String(), "fix\tskill:claude-code:acme-handbook\tfixed") {
		t.Fatalf("doctor reported manual skill removal:\n%s", stdout.String())
	}
}

func TestDoctorFixPreservesOpenCodeCompatibilityGlobalOrgSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	target := filepath.Join(home, ".config", "opencode", "skills", "acme-handbook")
	writeCLIManagedSkill(t, target, "acme:handbook")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "doctor", "--fix", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("doctor removed opencode compatibility global org skill: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Contains(stdout.String(), "skill:opencode:acme-handbook") {
		t.Fatalf("doctor reported opencode compatibility skill as legacy drift:\n%s", stdout.String())
	}
}

func TestDoctorReportsAbsentSelfSkillForPresentHarness(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "derived\tselfskill:claude-code\tabsent") ||
		!strings.Contains(out, "my skills self install") {
		t.Fatalf("doctor stdout = %q, want absent self-skill report", out)
	}
}

func TestDoctorSkipsSelfSkillForMissingHarnessDirs(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "selfskill:claude-code") {
		t.Fatalf("doctor stdout = %q, want missing harness dirs skipped", out)
	}
	if !strings.Contains(out, "derived\tselfskill\tok") {
		t.Fatalf("doctor stdout = %q, want self-skill ok line", out)
	}
}

func TestDoctorFixReinstallsAbsentSelfSkill(t *testing.T) {
	home, _, _, _ := setupCLITrackedManifest(t)
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"my", "doctor", "--fix", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tselfskill:claude-code\tfixed") {
		t.Fatalf("doctor stdout = %q, want self-skill fix", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "my-cli", "SKILL.md")); err != nil {
		t.Fatalf("self-skill not reinstalled: %v", err)
	}
}

func TestDoctorWithoutFixReportsWouldFixPlan(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "handbook", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "meetings", "2026-06-09-remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote meeting")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "would fast-forward") {
		t.Fatalf("doctor stdout = %q, want would-fast-forward plan", out)
	}
	if !strings.Contains(out, "my doctor --fix") {
		t.Fatalf("doctor stdout = %q, want doctor --fix hint", out)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "meetings", "2026-06-09-remote.md")); !os.IsNotExist(err) {
		t.Fatalf("doctor without --fix mutated the mount: %v", err)
	}
}

func TestDoctorFixFastForwardsCleanStaleMount(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "handbook", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "meetings", "2026-06-09-remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote meeting")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tacme:content:handbook\tfixed") ||
		!strings.Contains(out, "pulled --ff-only") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "meetings", "2026-06-09-remote.md")); err != nil {
		t.Fatalf("mount did not fast-forward: %v", err)
	}
}

func TestDoctorFixSkipsDirtyAndUnknownMounts(t *testing.T) {
	dirtyRemote, dirtyClone, dirtyWriter := setupCLIRemoteRepo(t, t.TempDir(), "dirty", map[string]string{"README.md": "seed\n"})
	unknownRemote, unknownClone, _ := setupCLIRemoteRepo(t, t.TempDir(), "unknown", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "dirty", "kind": "handbook", "git_url": "`+dirtyRemote+`", "mode": "required" },
    { "id": "unknown", "kind": "handbook", "git_url": "`+unknownRemote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	dirtyPath := filepath.Join(umbrellaRoot, "dirty")
	unknownPath := filepath.Join(umbrellaRoot, "unknown")
	if err := os.Rename(dirtyClone, dirtyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(unknownClone, unknownPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(dirtyWriter, "meetings", "remote.md"), "remote\n")
	commitAndPushCLIGit(t, dirtyWriter, "remote meeting")
	writeCLITestFile(t, filepath.Join(dirtyPath, "local.md"), "dirty\n")
	runCLIGit(t, unknownPath, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tacme:content:dirty\tskipped") ||
		!strings.Contains(out, "dirty checkout") ||
		!strings.Contains(out, "fix\tacme:content:unknown\tskipped") ||
		!strings.Contains(out, "remote freshness unknown") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dirtyPath, "meetings", "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("dirty mount was pulled despite skip: %v", err)
	}
}

func TestDoctorFreshnessCarriesSyncReasonAndNextCommand(t *testing.T) {
	result := syncer.Result{
		ID:          "handbook",
		Role:        "content",
		LocalPath:   "/tmp/acme/handbook",
		Status:      "held back",
		Message:     "unadopted untracked content file meetings/draft.md",
		ReasonCode:  "unadopted_content",
		NextCommand: "my record adopt meetings/draft.md",
	}
	item := doctorFreshnessItem(result, true, nil)
	if item.Status != "held back" || !strings.Contains(item.Message, "unadopted untracked content") {
		t.Fatalf("item = %#v, want held-back freshness item", item)
	}
	for _, want := range []string{"reason_code=unadopted_content", "next_command=my record adopt meetings/draft.md"} {
		if !containsString(item.Details, want) {
			t.Fatalf("details = %#v, missing %q", item.Details, want)
		}
	}

	detail := lastSyncResultDetail(result)
	for _, want := range []string{"reason_code=unadopted_content", "next_command=my record adopt meetings/draft.md"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("last sync detail = %q, missing %q", detail, want)
		}
	}
}

func TestDoctorSuppressesSelfReferentialNextCommand(t *testing.T) {
	result := syncer.Result{
		ID:          "handbook",
		Role:        "content",
		LocalPath:   "/tmp/acme/handbook",
		Status:      "held back",
		Message:     "detached HEAD",
		ReasonCode:  "detached_head",
		NextCommand: "my doctor",
	}
	item := doctorFreshnessItem(result, true, nil)
	if containsString(item.Details, "next_command=my doctor") {
		t.Fatalf("details = %#v, want self-referential next command suppressed", item.Details)
	}

	detail := lastSyncResultDetail(result)
	if strings.Contains(detail, "next_command=my doctor") {
		t.Fatalf("last sync detail = %q, want self-referential next command suppressed", detail)
	}
	if !strings.Contains(detail, "reason_code=detached_head") {
		t.Fatalf("last sync detail = %q, want reason code preserved", detail)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDoctorFixSkipsStaleProduct(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "product", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, manifestCache, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "repos.json"), `[
  { "id": "sample-product", "git_url": "`+remote+`" }
]`)
	_, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	state = umbrella.AddSelectedRepo(state, "sample-product")
	if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
	productPath := filepath.Join(umbrellaRoot, "products", "sample-product")
	if err := os.MkdirAll(filepath.Dir(productPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, productPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote product")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tacme:repo:repo:sample-product\tskipped") ||
		!strings.Contains(out, "repo checkouts are never fixed by doctor") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(productPath, "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("product was pulled despite skip: %v", err)
	}
}

func TestDoctorFixReconcilesDerivedArtifacts(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	umbrellaRoot := filepath.Join(home, "acme")
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLIManagedSkill(t, filepath.Join(home, ".claude", "skills", "acme-handbook"), "acme:handbook")
	writeCLITestFile(t, filepath.Join(umbrellaRoot, "AGENTS.md"), "<!-- my:generated workspace-guidance v1 -->\n\nstale\n")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tguidance\tfixed") ||
		!strings.Contains(out, "fix\tskill:claude-code:acme-handbook\tfixed") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("legacy global org skill was not removed: %v", err)
	}
}

func TestDoctorIncludesVersionItem(t *testing.T) {
	home := t.TempDir()
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource:         server.source(),
	}
	if err := a.run([]string{"my", "doctor", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse doctor JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Version) != 1 || report.Version[0].Status != "stale" ||
		!strings.Contains(report.Version[0].Message, "run my update") {
		t.Fatalf("version report = %#v", report.Version)
	}
}

func TestDoctorReportsLegacyFluxState(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "acme")
	if err := os.MkdirAll(filepath.Join(root, ".flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(home, ".config", "flux", "manifests.json"), `{"version":1}`)
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills", "flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	writeCLITestFile(t, filepath.Join(binDir, "flux"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(binDir, "flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("FLUX_HOME", filepath.Join(home, "old-flux"))

	server := newCLILatestServer(t, "0.1.0")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource:         server.source(),
	}
	if err := a.run([]string{"my", "doctor", "--home", home, "--umbrella", root, "--json"}); err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse doctor JSON: %v\n%s", err, stdout.String())
	}
	seen := map[string]bool{}
	for _, item := range report.Legacy {
		seen[item.Name] = true
	}
	for _, want := range []string{".flux", "flux data", "flux manifest registry", "FLUX_* env", "flux binary", "codex:flux skill"} {
		if !seen[want] {
			t.Fatalf("legacy items = %#v, missing %q", report.Legacy, want)
		}
	}
}

func TestDoctorReportsServiceHealth(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "services": [
    {
      "id": "ok-search",
      "kind": "mcp",
      "purpose": "Search",
      "auth_ref": "env://MYCLI_TEST_SET_TOKEN",
      "connection": { "type": "stdio", "command": "acme-docs-mcp" }
    },
    {
      "id": "unset-search",
      "kind": "mcp",
      "purpose": "Search with missing env",
      "auth_ref": "env://MYCLI_TEST_UNSET_TOKEN",
      "connection": { "type": "stdio", "command": "acme-docs-mcp" }
    },
    {
      "id": "remote-mcp",
      "kind": "mcp",
      "purpose": "Remote-only descriptor",
      "auth_ref": "none",
      "describe_ref": "https://mcp.example/server.json"
    },
    {
      "id": "broken-mcp",
      "kind": "mcp",
      "purpose": "Missing local descriptor",
      "auth_ref": "none",
      "describe_ref": "services/missing.server.json"
    }
  ]
}`)
	t.Setenv("MYCLI_TEST_SET_TOKEN", "set")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home, "--no-fetch"}); err != nil {
		t.Fatalf("doctor: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"service\tacme:ok-search\tok",
		"service\tacme:unset-search\twarning",
		"service\tacme:remote-mcp\twarning",
		"service\tacme:broken-mcp\terror",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor stdout missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "MYCLI_TEST_UNSET_TOKEN") {
		t.Fatalf("doctor should name the missing env var:\n%s", out)
	}
	if !strings.Contains(out, "services/missing.server.json") {
		t.Fatalf("doctor should name the missing descriptor:\n%s", out)
	}
}

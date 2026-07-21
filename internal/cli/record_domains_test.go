package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/outbox"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func TestRecordDomainAddListGetAndDurableOutbox(t *testing.T) {
	f := newPolicyTestFixture(t)
	configureRecordDomainMount(t, f)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	base := []string{"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot}
	args := append([]string{"my", "record", "add", "decisions", "choose-safe-default", "--title", "Choose safe default", "--field", "decision_type=security", "--no-publish", "--json"}, base...)
	if err := a.run(args); err != nil {
		t.Fatalf("record add: %v\n%s", err, stdout.String())
	}
	var added recordAddResult
	if err := json.Unmarshal(stdout.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if added.Publication.State != outbox.StateQueued || added.Record.Domain != "decisions" {
		t.Fatalf("result = %#v", added)
	}
	data, err := os.ReadFile(added.Record.Path)
	if err != nil || !strings.Contains(string(data), "decision_type: security") {
		t.Fatalf("record data: %v\n%s", err, data)
	}
	if status := gitCLIOutput(t, f.handbook, "status", "--porcelain", "--", "decisions"); !strings.Contains(status, " A decisions/") {
		t.Fatalf("record was not intent-to-add adopted: %q", status)
	}

	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "list", "decisions", "--json"}, base...)); err != nil || !strings.Contains(stdout.String(), added.Record.ID) {
		t.Fatalf("record list: %v\n%s", err, stdout.String())
	}
	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "get", "decisions", added.Record.ID}, base...)); err != nil || !strings.Contains(stdout.String(), "# Choose safe default") {
		t.Fatalf("record get: %v\n%s", err, stdout.String())
	}
	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "outbox", "--json"}, base...)); err != nil || !strings.Contains(stdout.String(), `"state": "queued"`) {
		t.Fatalf("record outbox: %v\n%s", err, stdout.String())
	}
}

func TestRecordDomainAutoPublishFailurePreservesRecordAndRetryState(t *testing.T) {
	f := newPolicyTestFixture(t)
	configureRecordDomainMount(t, f)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false)}
	err := a.run([]string{
		"my", "record", "add", "decisions", "offline-safe", "--manifest", "acme",
		"--home", f.home, "--umbrella", f.umbrellaRoot, "--json",
	})
	if err != nil {
		t.Fatalf("local record creation should survive publish failure: %v\n%s", err, stdout.String())
	}
	var result recordAddResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Publication.State != outbox.StateAttemptFailed || !strings.Contains(stderr.String(), "durable locally") {
		t.Fatalf("result=%#v stderr=%s", result, stderr.String())
	}
	if _, err := os.Stat(result.Record.Path); err != nil {
		t.Fatalf("publish failure lost record: %v", err)
	}
	events, err := os.ReadDir(filepath.Join(filepath.Dir(result.Publication.EventPath)))
	if err != nil || len(events) != 2 {
		t.Fatalf("append-only outbox events=%d err=%v", len(events), err)
	}
}

func TestRecordDomainAutoPublishSubmitsGovernedPR(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, baseHead := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	configureRecordDomainMount(t, f)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
		publishRunner: state.run,
	}
	if err := a.run([]string{
		"my", "record", "add", "decisions", "verified-publication", "--title", "Verified publication",
		"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot, "--json",
	}); err != nil {
		t.Fatalf("record auto publish: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	var result recordAddResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Publication.State != outbox.StateSubmitted || result.Publication.PRURL == "" ||
		result.Publication.PRHeadSHA == "" || result.Publication.PRBase != "master" ||
		len(result.Publication.PublishedPaths) == 0 || !state.created {
		t.Fatalf("result=%#v state=%#v", result, state)
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "rev-parse", "refs/heads/master")); got != baseHead {
		t.Fatalf("auto publication advanced protected base: %s != %s", got, baseHead)
	}
	if data, err := os.ReadFile(result.Record.Path); err != nil || !strings.Contains(string(data), "# Verified publication") {
		t.Fatalf("published record bytes missing: %v\n%s", err, data)
	}
}

func TestRecordDomainDigestDriftNeverSubmitsStaleOutboxItem(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, _ := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	configureRecordDomainMount(t, f)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false), publishRunner: state.run}
	base := []string{"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot}
	if err := a.run(append([]string{"my", "record", "add", "decisions", "digest-drift", "--no-publish", "--json"}, base...)); err != nil {
		t.Fatal(err)
	}
	var added recordAddResult
	if err := json.Unmarshal(stdout.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(added.Record.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\nchanged after queueing\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "flush", "--json"}, base...)); err != nil {
		t.Fatalf("flush: %v\n%s", err, stderr.String())
	}
	if state.createCalls != 0 {
		t.Fatal("digest drift opened a pull request")
	}
	events, err := outbox.List(f.umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]int{}
	for _, event := range events {
		states[event.State]++
	}
	if states[outbox.StateAttemptFailed] != 1 || states[outbox.StateQueued] != 1 {
		t.Fatalf("outbox states after drift = %#v; events=%#v", states, events)
	}
}

func TestRecordDomainManualFlushRequiresExplicitInclude(t *testing.T) {
	f := newPolicyTestFixture(t)
	setRecordDomainPublish(t, f, "manual-pr")
	remote, _ := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	configureRecordDomainMount(t, f)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false), publishRunner: state.run}
	base := []string{"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot}
	if err := a.run(append([]string{"my", "record", "add", "decisions", "manual-review", "--no-publish"}, base...)); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "flush"}, base...)); err != nil {
		t.Fatal(err)
	}
	if state.createCalls != 0 {
		t.Fatal("ordinary flush published manual-pr domain")
	}
	if err := a.run(append([]string{"my", "record", "flush", "--include-manual"}, base...)); err != nil {
		t.Fatal(err)
	}
	if state.createCalls != 1 {
		t.Fatalf("explicit manual flush create calls = %d", state.createCalls)
	}
}

func TestRecordDomainReconcileSkipsAbsentMountAndRecoversCrash(t *testing.T) {
	f := newPolicyTestFixture(t)
	addAbsentRecordDomain(t, f)
	configureRecordDomainMount(t, f)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	base := []string{"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot}
	if err := a.run(append([]string{"my", "record", "add", "decisions", "crash-recovery", "--no-publish", "--json"}, base...)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(outbox.Root(f.umbrellaRoot)); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "reconcile", "--json"}, base...)); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "record domain archive skipped") || !strings.Contains(stdout.String(), `"state": "queued"`) {
		t.Fatalf("stdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
}

func TestRecordDomainSubmittedTransitionsToMergedWithExactBlobProof(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, _ := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	configureRecordDomainMount(t, f)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false), publishRunner: state.run}
	base := []string{"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot}
	if err := a.run(append([]string{"my", "record", "add", "decisions", "merge-proof", "--json"}, base...)); err != nil {
		t.Fatal(err)
	}
	runCLIGit(t, remote, "update-ref", "refs/heads/master", state.commit)
	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "outbox", "--json"}, base...)); err != nil {
		t.Fatalf("outbox reconcile: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"state": "merged"`) || !strings.Contains(stdout.String(), `"merged_commit":`) {
		t.Fatalf("merged outbox=%s\nstderr=%s", stdout.String(), stderr.String())
	}
}

func TestAccessMonitorRetriesAutomaticRecordOutbox(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, _ := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	configureRecordDomainMount(t, f)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote}
	var stdout, stderr bytes.Buffer
	a := testAccessMonitorApp(app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
		publishRunner: state.run,
	}, f.home)
	base := []string{"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot}
	if err := a.run(append([]string{"my", "record", "add", "decisions", "monitor-retry", "--no-publish"}, base...)); err != nil {
		t.Fatal(err)
	}
	if state.createCalls != 0 {
		t.Fatal("record was published before monitor retry")
	}
	if err := a.run(append([]string{"my", "access", "monitor", "run"}, base...)); err != nil {
		t.Fatalf("monitor retry: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if state.createCalls != 1 {
		t.Fatalf("monitor PR create calls = %d", state.createCalls)
	}
}

func TestRecordDomainMixedPolicyBatchIsHeld(t *testing.T) {
	f := newPolicyTestFixture(t)
	addMixedPolicyRecordDomain(t, f)
	remote, _ := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	configureRecordDomainMount(t, f)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false), publishRunner: state.run}
	base := []string{"--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot}
	for _, spec := range [][2]string{{"decisions", "one"}, {"bugs", "two"}} {
		if err := a.run(append([]string{"my", "record", "add", spec[0], spec[1], "--no-publish"}, base...)); err != nil {
			t.Fatal(err)
		}
	}
	stdout.Reset()
	if err := a.run(append([]string{"my", "record", "flush"}, base...)); err != nil {
		t.Fatal(err)
	}
	if state.createCalls != 0 || !strings.Contains(stderr.String(), "different review or publication policies") {
		t.Fatalf("mixed policy batch was not held: calls=%d\nstderr=%s", state.createCalls, stderr.String())
	}
}

func setRecordDomainPublish(t *testing.T, f policyTestFixture, publish string) {
	t.Helper()
	path := filepath.Join(f.manifestCache, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), `"publish":"auto-pr"`, `"publish":"`+publish+`"`, 1)
	if updated == string(data) {
		t.Fatal("record domain publish policy was not replaced")
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAndPushCLIGit(t, f.manifestCache, "change record publication policy")
}

func addAbsentRecordDomain(t *testing.T, f policyTestFixture) {
	t.Helper()
	path := filepath.Join(f.manifestCache, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data),
		`{ "id": "handbook", "kind": "handbook", "git_url": "git@github.com:example/handbook.git", "mode": "required" }`,
		`{ "id": "handbook", "kind": "handbook", "git_url": "git@github.com:example/handbook.git", "mode": "required" },
    { "id": "archive", "kind": "handbook", "git_url": "git@github.com:example/archive.git", "mode": "optional" }`, 1)
	updated = strings.Replace(updated,
		`{"id":"decisions","title":"Decisions","mount":"handbook","path":"decisions","retention":"no-delete","admin_override":true,"review":"codeowner","publish":"auto-pr"}`,
		`{"id":"archive","title":"Archive","mount":"archive","path":"records","retention":"no-delete","review":"standard","publish":"auto-pr"},
      {"id":"decisions","title":"Decisions","mount":"handbook","path":"decisions","retention":"no-delete","admin_override":true,"review":"codeowner","publish":"auto-pr"}`, 1)
	if updated == string(data) {
		t.Fatal("absent record domain fixture was not updated")
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAndPushCLIGit(t, f.manifestCache, "add absent archive record domain")
}

func addMixedPolicyRecordDomain(t *testing.T, f policyTestFixture) {
	t.Helper()
	path := filepath.Join(f.manifestCache, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data),
		`{"id":"decisions","title":"Decisions","mount":"handbook","path":"decisions","retention":"no-delete","admin_override":true,"review":"codeowner","publish":"auto-pr"}`,
		`{"id":"decisions","title":"Decisions","mount":"handbook","path":"decisions","retention":"no-delete","admin_override":true,"review":"codeowner","publish":"auto-pr"},
      {"id":"bugs","title":"Bugs","mount":"handbook","path":"support/bugs","retention":"no-delete","review":"standard","publish":"auto-pr"}`, 1)
	if updated == string(data) {
		t.Fatal("mixed-policy fixture was not updated")
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAndPushCLIGit(t, f.manifestCache, "add mixed-policy record domain")
}

func configureRecordDomainMount(t *testing.T, f policyTestFixture) {
	t.Helper()
	_, state, err := umbrella.Ensure(f.umbrellaRoot, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	state.Mounts = []umbrella.MountStatus{{ID: "handbook", Kind: "handbook", SourceRef: "acme", Status: "synced"}}
	if err := umbrella.SaveState(f.umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
}

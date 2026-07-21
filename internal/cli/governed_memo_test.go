package cli

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
)

// One invocation must resolve the live GitHub actor at most once no matter
// how many governance gates consult it.
func TestGovernedMemoResolvesActorOncePerInvocation(t *testing.T) {
	f := newPolicyTestFixture(t)
	var userCalls int64
	counting := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if joined == "api user" {
			atomic.AddInt64(&userCalls, 1)
		}
		return governedAccessRunner(false)(name, args...)
	}
	a := app{stdout: nullWriter{}, stderr: nullWriter{}, accessRunner: counting, memo: newGovernedMemo()}
	ctx, err := loadPolicyContext(f.home, "acme", f.umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := a.governedPolicyActor(ctx.doc.doc); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt64(&userCalls); got != 1 {
		t.Fatalf("actor resolved %d times, want 1", got)
	}

	// Negative results are never memoized.
	failing := app{stdout: nullWriter{}, stderr: nullWriter{}, memo: newGovernedMemo(),
		accessRunner: func(string, ...string) ([]byte, error) { return nil, errFailingRunner }}
	if _, err := failing.governedPolicyActor(ctx.doc.doc); err == nil {
		t.Fatal("expected failure")
	}
	if _, ok := failing.memo.loadActor(); ok {
		t.Fatal("failure must not be memoized")
	}
}

func TestGovernedLaunchUsesOneActorCallAndOneCallPerRepository(t *testing.T) {
	f := newPolicyTestFixture(t)
	f.selectGovernedOperator(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	var userCalls, repositoryCalls int64
	base := governedAccessRunner(false)
	counting := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case joined == "api user":
			atomic.AddInt64(&userCalls, 1)
		case strings.HasPrefix(joined, "api -i repos/"):
			atomic.AddInt64(&repositoryCalls, 1)
		}
		return base(name, args...)
	}
	var stdout, stderr bytes.Buffer
	launched := false
	a := app{
		stdout: &stdout, stderr: &stderr, stdin: strings.NewReader(""), interactive: true,
		accessRunner: counting, memo: newGovernedMemo(),
		lookPath:    func(string) (string, error) { return "/bin/true", nil },
		execHarness: func(string, []string, string) error { launched = true; return nil },
	}
	if err := a.run([]string{
		"my", "ai", "--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot,
		"--no-session", "--no-refresh", "--no-update-check", "codex",
	}); err != nil {
		t.Fatalf("launch: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !launched {
		t.Fatal("harness did not launch")
	}
	if got := atomic.LoadInt64(&userCalls); got != 1 {
		t.Fatalf("GitHub actor calls = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&repositoryCalls); got != 2 {
		t.Fatalf("GitHub repository calls = %d, want one for each of two targets", got)
	}
}

func TestGovernedMemoDoesNotCacheRepositoryFailure(t *testing.T) {
	var userCalls, repositoryCalls int64
	runner := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case joined == "api user":
			atomic.AddInt64(&userCalls, 1)
			return []byte(`{"id":17,"node_id":"U_17","login":"operator"}`), nil
		case strings.HasPrefix(joined, "api -i repos/"):
			atomic.AddInt64(&repositoryCalls, 1)
			return []byte("HTTP/2.0 404 Not Found\r\n\r\n{}"), errFailingRunner
		default:
			t.Fatalf("unexpected provider call: %s %s", name, joined)
			return nil, errFailingRunner
		}
	}
	a := app{accessRunner: runner, memo: newGovernedMemo()}
	for i := 0; i < 2; i++ {
		decision := a.governedRepositoryDecision("https://github.com/example/private.git", access.Repository{})
		if decision.State == access.StateAllowed {
			t.Fatalf("attempt %d unexpectedly allowed", i+1)
		}
	}
	if got := atomic.LoadInt64(&userCalls); got != 1 {
		t.Fatalf("GitHub actor calls = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&repositoryCalls); got != 2 {
		t.Fatalf("failed repository calls = %d, want 2 because failures are not cached", got)
	}
}

func TestGovernedMemoCoalescesConcurrentRepositoryChecks(t *testing.T) {
	var repositoryCalls int64
	started := make(chan struct{})
	release := make(chan struct{})
	runner := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if !strings.HasPrefix(joined, "api -i repos/") {
			t.Fatalf("unexpected provider call: %s %s", name, joined)
		}
		if atomic.AddInt64(&repositoryCalls, 1) == 1 {
			close(started)
		}
		<-release
		return []byte("HTTP/2.0 200 OK\r\n\r\n" +
			`{"id":23,"node_id":"R_23","full_name":"example/private","private":true,"permissions":{"pull":true}}`), nil
	}
	a := app{accessRunner: runner, memo: newGovernedMemo()}
	actor := access.Actor{ID: 17, NodeID: "U_17", Login: "operator"}
	var wg sync.WaitGroup
	results := make(chan access.Decision, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- a.governedRepositoryDecisionForActor("https://github.com/example/private.git", access.Repository{}, actor)
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	close(release)
	wg.Wait()
	close(results)
	for decision := range results {
		if !decision.Allows(access.PermissionRead) {
			t.Fatalf("coalesced decision = %#v, want readable", decision)
		}
	}
	if got := atomic.LoadInt64(&repositoryCalls); got != 1 {
		t.Fatalf("concurrent repository calls = %d, want 1", got)
	}
}

func TestGovernedMemoPreservesExpiredBaselineObservations(t *testing.T) {
	body := strings.Replace(governedAccessTestManifest(), `"access": {`, `"access": { "positive_ttl": "1ns",`, 1)
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	targets := materializeAccessTargetsForTest(t, home)
	activator := testAccessMonitorApp(app{
		stdout: nullWriter{}, stderr: nullWriter{}, accessRunner: governedAccessRunner(false),
	}, home)
	if err := activator.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	before, err := access.LoadInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	checkedBefore := map[string]time.Time{}
	for _, target := range targets {
		entry, ok := managedInventoryEntry(before, target.Path)
		if !ok {
			t.Fatalf("activated target missing from inventory: %s", target.Path)
		}
		baseline, ok := newestPositiveBaseline(entry.Baselines)
		if !ok {
			t.Fatalf("activated target missing baseline: %s", target.Path)
		}
		checkedBefore[target.Path], err = time.Parse(time.RFC3339Nano, baseline.CheckedAt)
		if err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Millisecond)

	doc, err := loadSingleRegisteredDoc(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	var userCalls, repositoryCalls int64
	base := governedAccessRunner(false)
	counting := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case joined == "api user":
			atomic.AddInt64(&userCalls, 1)
		case strings.HasPrefix(joined, "api -i repos/"):
			atomic.AddInt64(&repositoryCalls, 1)
		}
		return base(name, args...)
	}
	a := app{accessRunner: counting, memo: newGovernedMemo()}
	for gate := 0; gate < 2; gate++ {
		if err := a.requireGovernedLaunchAccess(home, doc, root); err != nil {
			t.Fatalf("gate %d: %v", gate+1, err)
		}
	}
	if got := atomic.LoadInt64(&userCalls); got != 1 {
		t.Fatalf("GitHub actor calls = %d, want 1 across repeated gates", got)
	}
	if got := atomic.LoadInt64(&repositoryCalls); got != int64(len(targets)) {
		t.Fatalf("GitHub repository calls = %d, want one per %d target repositories", got, len(targets))
	}

	after, err := access.LoadInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		entry, ok := managedInventoryEntry(after, target.Path)
		if !ok {
			t.Fatalf("observed target missing from inventory: %s", target.Path)
		}
		baseline, ok := newestPositiveBaseline(entry.Baselines)
		if !ok {
			t.Fatalf("observed target missing baseline: %s", target.Path)
		}
		checkedAfter, parseErr := time.Parse(time.RFC3339Nano, baseline.CheckedAt)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if !checkedAfter.After(checkedBefore[target.Path]) {
			t.Fatalf("expired baseline was not refreshed for %s", target.Path)
		}
		if len(entry.Revocations) == 0 || entry.Revocations[0].LastState != access.StateAllowed {
			t.Fatalf("positive live observation not persisted for %s: %#v", target.Path, entry.Revocations)
		}
	}
}

type nullWriter struct{}

func (nullWriter) Write(p []byte) (int, error) { return len(p), nil }

var errFailingRunner = &failingRunnerError{}

type failingRunnerError struct{}

func (*failingRunnerError) Error() string { return "provider offline" }

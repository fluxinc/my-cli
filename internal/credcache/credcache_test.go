//go:build darwin || linux

package credcache

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// startBroker serves a real socket in a home, so clients take their normal path.
func startBroker(t *testing.T, home string) (*Server, *fakeClock, func()) {
	t.Helper()
	dir, err := runtimeDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, socketName))
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	server := NewServer()
	server.Now = clock.Now
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(ctx, listener)
	}()
	stop := func() { cancel(); <-done }
	t.Cleanup(stop)
	return server, clock, sync.OnceFunc(stop)
}

var testKey = Key{ManifestName: "acme", Manifest: "m", Service: "registry", URL: "https://x", Ref: "op://v/i/c", Account: "A"}

func testHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "cc")
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return home
}

func resolver(calls *atomic.Int32, token string) func() (string, error) {
	return func() (string, error) {
		calls.Add(1)
		return token, nil
	}
}

func TestColdThenWarmFetchesOnce(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	client := &Client{Home: home}
	var calls atomic.Int32
	req := Request{Key: testKey, Resolve: resolver(&calls, "fake-key")}
	first, err := client.Get(req, Settings{})
	if err != nil || !first.Fetched || !first.Cached || first.Token != "fake-key" {
		t.Fatalf("first %+v %v", first, err)
	}
	second, err := client.Get(req, Settings{})
	if err != nil || second.Fetched || second.Token != "fake-key" || calls.Load() != 1 {
		t.Fatalf("second %+v %v calls=%d", second, err, calls.Load())
	}
}

func TestConcurrentMissesShareOneFetch(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	var calls atomic.Int32
	req := Request{Key: testKey, Resolve: func() (string, error) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		return "fake-key", nil
	}}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g, err := (&Client{Home: home}).Get(req, Settings{}); err != nil || g.Token != "fake-key" {
				t.Errorf("%+v %v", g, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestExpiryIdleAndHard(t *testing.T) {
	home := testHome(t)
	_, clock, _ := startBroker(t, home)
	client := &Client{Home: home}
	var calls atomic.Int32
	req := Request{Key: testKey, Resolve: resolver(&calls, "fake-key")}
	settings := Settings{Idle: "1h", Max: "2h"}
	if _, err := client.Get(req, settings); err != nil {
		t.Fatal(err)
	}
	// Use keeps it past one idle period, but never past the hard limit.
	for range 3 {
		clock.Advance(50 * time.Minute)
		if _, err := client.Get(req, settings); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("hard limit: calls = %d", calls.Load())
	}
	clock.Advance(61 * time.Minute)
	if _, err := client.Get(req, settings); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("idle limit: calls = %d", calls.Load())
	}
}

func TestNotAfterBoundsTheLease(t *testing.T) {
	home := testHome(t)
	_, clock, _ := startBroker(t, home)
	client := &Client{Home: home}
	var calls atomic.Int32
	req := Request{Key: testKey, NotAfter: clock.Now().Add(10 * time.Minute), Resolve: resolver(&calls, "fake-key")}
	grant, err := client.Get(req, Settings{})
	if err != nil || !grant.HardUntil.Equal(req.NotAfter) {
		t.Fatalf("%+v %v", grant, err)
	}
}

func TestLockFencesAFetchUnderWay(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	client := &Client{Home: home}
	started, release := make(chan struct{}), make(chan struct{})
	req := Request{Key: testKey, Resolve: func() (string, error) {
		close(started)
		<-release
		return "fake-before-lock", nil
	}}
	result := make(chan Grant, 1)
	go func() {
		g, err := client.Get(req, Settings{})
		if err != nil {
			t.Error(err)
		}
		result <- g
	}()
	<-started
	if err := client.Lock(); err != nil {
		t.Fatal(err)
	}
	close(release)
	g := <-result
	if g.Cached || !g.Fetched || g.Token != "fake-before-lock" {
		t.Fatalf("grant %+v", g)
	}
	entries, err := client.Status()
	if err != nil || len(entries) != 0 {
		t.Fatalf("cache refilled after lock: %+v %v", entries, err)
	}
}

func TestLockRejectsOldLeaseEvenAfterNewFetchStarts(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	client := &Client{Home: home}
	oldStarted, oldRelease := make(chan struct{}), make(chan struct{})
	result := make(chan Grant, 1)
	go func() {
		g, _ := client.Get(Request{Key: testKey, Resolve: func() (string, error) {
			close(oldStarted)
			<-oldRelease
			return "fake-old", nil
		}}, Settings{})
		result <- g
	}()
	<-oldStarted
	if err := client.Lock(); err != nil {
		t.Fatal(err)
	}
	newStarted, newRelease := make(chan struct{}), make(chan struct{})
	fresh := make(chan Grant, 1)
	go func() {
		g, _ := client.Get(Request{Key: testKey, Resolve: func() (string, error) {
			close(newStarted)
			<-newRelease
			return "fake-new", nil
		}}, Settings{})
		fresh <- g
	}()
	<-newStarted
	close(oldRelease)
	if g := <-result; g.Cached {
		t.Fatal("old lease published after lock")
	}
	close(newRelease)
	if g := <-fresh; !g.Cached || g.Token != "fake-new" {
		t.Fatalf("new fetch %+v", g)
	}
}

func TestWaiterDuringLockDoesNotFetch(t *testing.T) {
	home := testHome(t)
	server, _, _ := startBroker(t, home)
	client := &Client{Home: home}
	started, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = client.Get(Request{Key: testKey, Resolve: func() (string, error) {
			close(started)
			<-release
			return "fake-key", nil
		}}, Settings{})
	}()
	<-started
	defer close(release)
	var calls atomic.Int32
	waited := make(chan error, 1)
	go func() {
		_, err := client.Get(Request{Key: testKey, Resolve: resolver(&calls, "fake-waiter")}, Settings{})
		waited <- err
	}()
	waitFor(t, func() bool { return server.waiters() > 0 })
	if err := client.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := <-waited; err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("waiter: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("waiter fetched after lock")
	}
}

func TestDenialCoolsDownAndLockClearsIt(t *testing.T) {
	home := testHome(t)
	_, clock, _ := startBroker(t, home)
	client := &Client{Home: home}
	var calls atomic.Int32
	denied := errors.New("auth_ref x: op read failed (exit 1)")
	req := Request{Key: testKey, Resolve: func() (string, error) {
		calls.Add(1)
		return "", denied
	}}
	if _, err := client.Get(req, Settings{}); !errors.Is(err, denied) {
		t.Fatalf("first: %v", err)
	}
	var cool *CooldownError
	if _, err := client.Get(req, Settings{}); !errors.As(err, &cool) || cool.RetryAfter <= 0 {
		t.Fatalf("second: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
	clock.Advance(DenyCooldown + time.Second)
	if _, err := client.Get(req, Settings{}); !errors.Is(err, denied) {
		t.Fatalf("after cooldown: %v", err)
	}
	if err := client.Lock(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(req, Settings{}); !errors.Is(err, denied) || calls.Load() != 3 {
		t.Fatalf("lock should clear the cooldown: %v calls=%d", err, calls.Load())
	}
}

func TestDeadOwnerPromotesOneWaiter(t *testing.T) {
	home := testHome(t)
	server, _, _ := startBroker(t, home)
	client := &Client{Home: home}
	conn, err := client.dial()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := conn.call(request{Op: "acquire", Key: testKey.ID()})
	if err != nil || resp.State != stateResolve {
		t.Fatalf("%+v %v", resp, err)
	}
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := (&Client{Home: home}).Get(Request{Key: testKey, Resolve: func() (string, error) {
				calls.Add(1)
				time.Sleep(50 * time.Millisecond)
				return "fake-key", nil
			}}, Settings{})
			if err != nil || g.Token != "fake-key" {
				t.Errorf("%+v %v", g, err)
			}
		}()
	}
	waitFor(t, func() bool { return server.waiters() >= 5 })
	conn.conn.Close()
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestWaiterTimeoutNeverFetches(t *testing.T) {
	home := testHome(t)
	server, _, _ := startBroker(t, home)
	server.WaitTimeout = 50 * time.Millisecond
	client := &Client{Home: home}
	conn, err := client.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.conn.Close()
	if resp, err := conn.call(request{Op: "acquire", Key: testKey.ID()}); err != nil || resp.State != stateResolve {
		t.Fatalf("%+v %v", resp, err)
	}
	var calls atomic.Int32
	_, err = client.Get(Request{Key: testKey, Resolve: resolver(&calls, "fake-key")}, Settings{})
	if err == nil || !strings.Contains(err.Error(), "timed out") || calls.Load() != 0 {
		t.Fatalf("%v calls=%d", err, calls.Load())
	}
}

func TestInvalidateIsGenerationSafeAcrossRestart(t *testing.T) {
	home := testHome(t)
	_, _, stop := startBroker(t, home)
	client := &Client{Home: home}
	var calls atomic.Int32
	req := Request{Key: testKey, Resolve: resolver(&calls, "fake-key")}
	old, err := client.Get(req, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	os.Remove(mustSocket(t, client))
	startBroker(t, home)
	fresh, err := client.Get(req, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Invalidate(testKey, old.Generation); err != nil {
		t.Fatal(err)
	}
	again, err := client.Get(req, Settings{})
	if err != nil || again.Fetched || again.Generation != fresh.Generation {
		t.Fatalf("stale generation evicted the new key: %+v %v", again, err)
	}
	if err = client.Invalidate(testKey, fresh.Generation); err != nil {
		t.Fatal(err)
	}
	if g, err := client.Get(req, Settings{}); err != nil || !g.Fetched {
		t.Fatalf("%+v %v", g, err)
	}
}

func TestStatusNeverCarriesAKey(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	client := &Client{Home: home}
	var calls atomic.Int32
	if _, err := client.Get(Request{Key: testKey, Resolve: resolver(&calls, "fake-secret-value")}, Settings{}); err != nil {
		t.Fatal(err)
	}
	conn, err := client.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.conn.Close()
	resp, err := conn.call(request{Op: "status"})
	if err != nil || len(resp.Entries) != 1 || resp.Token != "" || resp.Entries[0].Service != "registry" {
		t.Fatalf("%+v %v", resp, err)
	}
}

func TestStalledBrokerFailsWithinBound(t *testing.T) {
	home := testHome(t)
	dir, _ := runtimeDir(home)
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, socketName))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close() // accepts, then never answers
		}
	}()
	start := time.Now()
	var calls atomic.Int32
	_, err = (&Client{Home: home}).Get(Request{Key: testKey, Resolve: resolver(&calls, "fake-key")}, Settings{})
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || calls.Load() != 0 {
		t.Fatalf("%v calls=%d", err, calls.Load())
	}
	if elapsed := time.Since(start); elapsed > 3*callTimeout {
		t.Fatalf("took %s", elapsed)
	}
}

func TestPrivateDirRefusals(t *testing.T) {
	root := testHome(t)
	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateDir(open); err == nil {
		t.Fatal("0755 accepted")
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateDir(link); err == nil {
		t.Fatal("symlink accepted")
	}
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(shared, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateDir(inner); err == nil {
		t.Fatal("world-writable ancestor accepted")
	}
	if err := verifyPrivateDir(target); err != nil {
		t.Fatal(err)
	}
}

func TestPeerRefusedClosesConnection(t *testing.T) {
	home := testHome(t)
	server, _, _ := startBroker(t, home)
	server.mu.Lock()
	server.PeerAllowed = func(net.Conn) bool { return false }
	server.mu.Unlock()
	var calls atomic.Int32
	_, err := (&Client{Home: home}).Get(Request{Key: testKey, Resolve: resolver(&calls, "fake-key")}, Settings{})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("%v calls=%d", err, calls.Load())
	}
}

func TestPeerCredentialIsThisUser(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	conn, err := (&Client{Home: home}).dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.conn.Close()
	if !peerIsCurrentUser(conn.conn) {
		t.Fatal("peer check rejected this user")
	}
}

func TestRunBrokerSingletonAndStaleSocket(t *testing.T) {
	home := testHome(t)
	dir, _ := runtimeDir(home)
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	// A leftover socket from a crash: bound, then abandoned without unlinking.
	stale, err := net.Listen("unix", filepath.Join(dir, socketName))
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- RunBroker(ctx, home) }()
	client := &Client{Home: home}
	waitFor(t, func() bool {
		conn, err := client.dial()
		if err != nil {
			return false
		}
		conn.conn.Close()
		return true
	})
	// A second broker finds the lock held and leaves the live socket alone.
	if err := RunBroker(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	conn, err := client.dial()
	if err != nil {
		t.Fatalf("second broker disturbed the first: %v", err)
	}
	conn.conn.Close()
	cancel()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestOversizedFrameIsRefused(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	conn, err := (&Client{Home: home}).dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.conn.Close()
	_ = conn.conn.SetDeadline(time.Now().Add(5 * time.Second))
	go conn.conn.Write([]byte(strings.Repeat("x", maxFrame+4096)))
	if _, err = readFrame(conn.reader); err == nil {
		t.Fatal("broker answered an oversized frame")
	}
}

func TestSettingsLimitsClamp(t *testing.T) {
	idle, hard := Settings{Idle: "48h", Max: "100h"}.Limits()
	if hard != DefaultMax || idle != DefaultIdle {
		t.Fatalf("%s %s", idle, hard)
	}
	idle, hard = Settings{Idle: "30m", Max: "2h"}.Limits()
	if idle != 30*time.Minute || hard != 2*time.Hour {
		t.Fatalf("%s %s", idle, hard)
	}
}

func TestKeyIdentityCoversEveryField(t *testing.T) {
	seen := map[string]bool{testKey.ID(): true}
	for _, changed := range []Key{
		{Manifest: "x", Service: "registry", URL: "https://x", Ref: "op://v/i/c", Account: "A"},
		{Manifest: "m", Service: "x", URL: "https://x", Ref: "op://v/i/c", Account: "A"},
		{Manifest: "m", Service: "registry", URL: "https://y", Ref: "op://v/i/c", Account: "A"},
		{Manifest: "m", Service: "registry", URL: "https://x", Ref: "op://v/i/d", Account: "A"},
		{Manifest: "m", Service: "registry", URL: "https://x", Ref: "op://v/i/c", Account: "B"},
	} {
		if seen[changed.ID()] {
			t.Fatalf("%+v collides", changed)
		}
		seen[changed.ID()] = true
	}
}

func mustSocket(t *testing.T, c *Client) string {
	t.Helper()
	path, err := c.socketPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUnconditionalInvalidateFencesAFetchUnderWay(t *testing.T) {
	home := testHome(t)
	startBroker(t, home)
	client := &Client{Home: home}
	started, release := make(chan struct{}), make(chan struct{})
	result := make(chan Grant, 1)
	go func() {
		g, _ := client.Get(Request{Key: testKey, Resolve: func() (string, error) {
			close(started)
			<-release
			return "fake-old", nil
		}}, Settings{})
		result <- g
	}()
	<-started
	if err := client.Invalidate(testKey, 0); err != nil {
		t.Fatal(err)
	}
	close(release)
	if g := <-result; g.Cached {
		t.Fatal("fenced fetch was published")
	}
}

func TestTighterNotAfterBoundsAHit(t *testing.T) {
	home := testHome(t)
	_, clock, _ := startBroker(t, home)
	client := &Client{Home: home}
	var calls atomic.Int32
	req := Request{Key: testKey, Resolve: resolver(&calls, "fake-key")}
	if _, err := client.Get(req, Settings{}); err != nil {
		t.Fatal(err)
	}
	req.NotAfter = clock.Now().Add(time.Minute)
	g, err := client.Get(req, Settings{})
	if err != nil || g.Fetched || !g.HardUntil.Equal(req.NotAfter) {
		t.Fatalf("%+v %v", g, err)
	}
}

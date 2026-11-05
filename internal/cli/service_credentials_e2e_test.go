//go:build darwin || linux

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/credcache"
)

const (
	e2eKeyOne = "fake-e2e-key-one"
	e2eKeyTwo = "fake-e2e-key-two"
)

var (
	e2eBinaryOnce sync.Once
	e2eBinaryPath string
	e2eBinaryErr  error
)

// e2eBinary builds the real my once, so these tests cross real process
// boundaries: command, detached broker and stub op.
func e2eBinary(t *testing.T) string {
	t.Helper()
	e2eBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "my-e2e-bin")
		if err != nil {
			e2eBinaryErr = err
			return
		}
		e2eBinaryPath = filepath.Join(dir, "my")
		// testenv moved the working directory out of the module.
		_, source, _, _ := runtime.Caller(0)
		build := exec.Command("go", "build", "-o", e2eBinaryPath, "./cmd/my")
		build.Dir = filepath.Join(filepath.Dir(source), "..", "..")
		if out, err := build.CombinedOutput(); err != nil {
			e2eBinaryErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if e2eBinaryErr != nil {
		t.Fatal(e2eBinaryErr)
	}
	return e2eBinaryPath
}

// e2eWorld is a sandboxed home with a stub op, a stub service and an op://
// service reference.
type e2eWorld struct {
	t       *testing.T
	home    string
	bin     string
	stubDir string
	opLog   string
	current atomic.Value // the key the service accepts
	forbid  atomic.Bool
	server  *httptest.Server
}

func newE2EWorld(t *testing.T) *e2eWorld {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "my-e2e") // short enough for a socket path
	if err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.EvalSymlinks(root)
	w := &e2eWorld{t: t, home: filepath.Join(root, "h"), bin: e2eBinary(t), stubDir: filepath.Join(root, "stub"), opLog: filepath.Join(root, "op.log")}
	w.current.Store(e2eKeyOne)
	for _, dir := range []string{w.home, w.stubDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	w.setKey(e2eKeyOne)
	w.server = httptest.NewServer(http.HandlerFunc(w.serve))
	cache := filepath.Join(w.home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(cache, "manifest.json"), fmt.Sprintf(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "workspaces": [
    { "id": "handbook", "git_url": "https://github.com/acme/acme-handbook.git", "local_path": "~/.my-cli/workspaces/handbook" }
  ],
  "services": [
    { "id": "registry", "kind": "http", "purpose": "Instance registry", "auth_ref": "op://Shared/Registry/credential",
      "connection": { "type": "http", "url": %q } }
  ],
  "data_bindings": { "fleet": { "surfaces": ["service:registry", "mount:handbook"] } }
}`, w.server.URL+"/api/v1"))
	writeCLITestFile(t, filepath.Join(w.home, ".my-cli", "workspaces", "handbook", "fleet", "README.md"), "# Fleet\n")
	writeCLITestFile(t, filepath.Join(w.home, ".config", "my-cli", "manifests.json"), fmt.Sprintf(`{"version":1,"manifests":[{"name":"acme","git_url":"https://example.test/acme.git","local_path":%q}]}`, cache))
	t.Cleanup(func() {
		w.run("services", "auth", "lock", "--home", w.home)
		w.killBroker()
		w.server.Close()
		os.RemoveAll(root)
	})
	return w
}

// setKey rewrites the stub op: it logs each read and prints the current key.
// A file named deny makes it fail like a cancelled prompt; sleep slows it down.
func (w *e2eWorld) setKey(key string) {
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = account ]; then
  [ -f %[1]q ] && { cat %[1]q; exit 0; }
  echo '[{"account_uuid":"ACCT1","user_uuid":"U1","email":"a@example.test","url":"a.example.test"}]'
  exit 0
fi
echo "$*" >> %[2]q
[ -f %[3]q ] && exit 1
[ -f %[4]q ] && sleep 1
printf %%s %[5]q
`, filepath.Join(w.stubDir, "accounts.json"), w.opLog, filepath.Join(w.stubDir, "deny"), filepath.Join(w.stubDir, "sleep"), key)
	path := filepath.Join(w.stubDir, "op")
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(script), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		w.t.Fatal(err)
	}
}

func (w *e2eWorld) serve(rw http.ResponseWriter, r *http.Request) {
	if w.forbid.Load() {
		rw.WriteHeader(403)
		fmt.Fprint(rw, `{"error":"forbidden"}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+w.current.Load().(string) {
		rw.WriteHeader(401)
		fmt.Fprint(rw, `{"error":"unauthorized"}`)
		return
	}
	switch {
	case r.URL.Path == "/api/v1/me":
		json.NewEncoder(rw).Encode(map[string]any{"issuer": map[string]any{"name": "Fictional"}, "token": map[string]any{"name": "Laptop", "scopes": []string{"read", "write"}, "expires_at": time.Now().Add(30 * 24 * time.Hour)}})
	default:
		fmt.Fprint(rw, `{"records":[],"next_page":null}`)
	}
}

func (w *e2eWorld) command(args ...string) *exec.Cmd {
	cmd := exec.Command(w.bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+w.home, "PATH="+w.stubDir+":"+os.Getenv("PATH"), "MYCLI_NO_UPDATE_CHECK=1", "MYCLI_DISABLE_SELF_SKILL_SYNC=1", "MYCLI_NO_CRED_CACHE=", "OP_ACCOUNT=", "OP_SERVICE_ACCOUNT_TOKEN=", "OP_CONNECT_TOKEN=")
	cmd.Dir = w.home
	return cmd
}

func (w *e2eWorld) run(args ...string) (string, string, error) {
	cmd := w.command(args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (w *e2eWorld) search() (string, string, error) {
	return w.run("fleet", "search", "box", "--home", w.home, "--manifest", "acme")
}

func (w *e2eWorld) mustSearch() string {
	w.t.Helper()
	_, stderr, err := w.search()
	if err != nil {
		w.t.Fatalf("search: %v\n%s", err, stderr)
	}
	return stderr
}

func (w *e2eWorld) opReads() int {
	data, err := os.ReadFile(w.opLog)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// killBroker ends the broker of this home, found by its command line.
func (w *e2eWorld) killBroker() bool {
	return killBrokerFor(w.t, w.bin, w.home)
}

func killBrokerFor(t *testing.T, bin, home string) bool {
	killed := false
	for _, pid := range brokerPIDs(t, bin, home) {
		if syscall.Kill(pid, syscall.SIGKILL) == nil {
			killed = true
		}
	}
	return killed
}

func brokerPIDs(t *testing.T, bin, home string) []int {
	t.Helper()
	out, err := exec.Command("ps", "-axwwo", "pid=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(line, bin) || !strings.Contains(line, "auth "+credBrokerOperation) || !strings.Contains(line, home) {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func TestE2ESequentialCommandsAskOnce(t *testing.T) {
	w := newE2EWorld(t)
	first := w.mustSearch()
	if !strings.Contains(first, "registry key cached") || !strings.Contains(first, "after 1Password locks") || !strings.Contains(first, "my services auth lock") {
		t.Fatalf("first-use notice: %q", first)
	}
	for range 5 {
		if stderr := w.mustSearch(); strings.Contains(stderr, "cached in memory") {
			t.Fatalf("notice repeated: %q", stderr)
		}
	}
	if n := w.opReads(); n != 1 {
		t.Fatalf("op reads = %d", n)
	}
	data, _ := os.ReadFile(w.opLog)
	if !strings.Contains(string(data), "--account ACCT1") {
		t.Fatalf("read not bound to the account: %s", data)
	}
}

func TestE2EConcurrentCommandsAskOnce(t *testing.T) {
	w := newE2EWorld(t)
	writeCLITestFile(t, filepath.Join(w.stubDir, "sleep"), "")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, stderr, err := w.search(); err != nil {
				t.Errorf("%v: %s", err, stderr)
			}
		}()
	}
	wg.Wait()
	if n := w.opReads(); n != 1 {
		t.Fatalf("op reads = %d", n)
	}
}

func TestE2ELockRefreshAndOffControlFetching(t *testing.T) {
	w := newE2EWorld(t)
	w.mustSearch()
	if _, stderr, err := w.run("services", "auth", "lock", "--home", w.home); err != nil {
		t.Fatal(stderr)
	}
	w.mustSearch()
	if n := w.opReads(); n != 2 {
		t.Fatalf("after lock: op reads = %d", n)
	}
	if _, stderr, err := w.run("services", "auth", "refresh", "registry", "--home", w.home); err != nil {
		t.Fatal(stderr)
	}
	if n := w.opReads(); n != 3 {
		t.Fatalf("after refresh: op reads = %d", n)
	}
	status, stderr, err := w.run("services", "auth", "cache", "status", "--home", w.home)
	if err != nil || !strings.Contains(status, "registry: cached until") || strings.Contains(status, e2eKeyOne) {
		t.Fatalf("status %q %v %s", status, err, stderr)
	}
	if _, stderr, err := w.run("services", "auth", "cache", "off", "--home", w.home); err != nil {
		t.Fatal(stderr)
	}
	w.mustSearch()
	w.mustSearch()
	if n := w.opReads(); n != 5 {
		t.Fatalf("cache off: op reads = %d", n)
	}
	status, _, _ = w.run("services", "auth", "cache", "--home", w.home)
	if !strings.Contains(status, "Credential cache: off") || strings.Contains(status, "registry:") {
		t.Fatalf("off status %q", status)
	}
}

func TestE2ENoCacheFlagAndEnvironmentAskDirectly(t *testing.T) {
	w := newE2EWorld(t)
	if _, stderr, err := w.run("fleet", "search", "box", "--no-cache", "--home", w.home, "--manifest", "acme"); err != nil {
		t.Fatal(stderr)
	}
	cmd := w.command("fleet", "search", "box", "--home", w.home, "--manifest", "acme")
	cmd.Env = append(cmd.Env, "MYCLI_NO_CRED_CACHE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	if n := w.opReads(); n != 2 {
		t.Fatalf("op reads = %d", n)
	}
	if w.killBroker() {
		t.Fatal("direct mode started a broker")
	}
	data, _ := os.ReadFile(w.opLog)
	if strings.Contains(string(data), "--account") {
		t.Fatalf("direct mode changed the op call: %s", data)
	}
}

func TestE2ERotationRefreshesReadsOnce(t *testing.T) {
	w := newE2EWorld(t)
	w.mustSearch()
	// The service rotates; the stale cached key is replaced on the next read.
	w.setKey(e2eKeyTwo)
	w.current.Store(e2eKeyTwo)
	w.mustSearch()
	if n := w.opReads(); n != 2 {
		t.Fatalf("read refresh: op reads = %d", n)
	}
	w.mustSearch()
	if n := w.opReads(); n != 2 {
		t.Fatalf("after refresh: op reads = %d", n)
	}
}

func TestE2EForbiddenNeverRefetches(t *testing.T) {
	w := newE2EWorld(t)
	w.mustSearch()
	w.forbid.Store(true)
	if _, _, err := w.search(); err == nil {
		t.Fatal("403 passed")
	}
	if n := w.opReads(); n != 1 {
		t.Fatalf("op reads = %d", n)
	}
}

func TestE2EDeniedPromptCoolsDownAcrossProcesses(t *testing.T) {
	w := newE2EWorld(t)
	writeCLITestFile(t, filepath.Join(w.stubDir, "deny"), "")
	if _, stderr, err := w.search(); err == nil || !strings.Contains(stderr, "op read failed") {
		t.Fatalf("%v %s", err, stderr)
	}
	_, stderr, err := w.search()
	if err == nil || !strings.Contains(stderr, "not asking 1Password again") || !strings.Contains(stderr, "--no-cache") {
		t.Fatalf("%v %s", err, stderr)
	}
	if n := w.opReads(); n != 1 {
		t.Fatalf("op reads = %d", n)
	}
	os.Remove(filepath.Join(w.stubDir, "deny"))
	if _, stderr, err := w.run("services", "auth", "lock", "--home", w.home); err != nil {
		t.Fatal(stderr)
	}
	w.mustSearch()
}

func TestE2EKilledBrokerRestartsAndRefetches(t *testing.T) {
	w := newE2EWorld(t)
	w.mustSearch()
	if !w.killBroker() {
		t.Fatal("no broker to kill")
	}
	w.mustSearch()
	if n := w.opReads(); n != 2 {
		t.Fatalf("op reads = %d", n)
	}
	w.mustSearch()
	if n := w.opReads(); n != 2 {
		t.Fatalf("restarted broker did not cache: op reads = %d", n)
	}
}

func TestE2EUnreachableBrokerErrorsWithEscape(t *testing.T) {
	w := newE2EWorld(t)
	// A runtime directory others can enter is refused, and no fallback prompts.
	dir := filepath.Join(w.home, ".config", "my-cli", "credcache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := w.search()
	if err == nil || !strings.Contains(stderr, "credential cache unavailable") || !strings.Contains(stderr, "--no-cache") {
		t.Fatalf("%v %s", err, stderr)
	}
	if n := w.opReads(); n != 0 {
		t.Fatalf("op reads = %d", n)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestE2EAccountSelection(t *testing.T) {
	w := newE2EWorld(t)
	accounts := filepath.Join(w.stubDir, "accounts.json")
	writeCLITestFile(t, accounts, `[{"account_uuid":"ACCT1","email":"same@example.test","url":"one.example.test"},{"account_uuid":"ACCT2","email":"same@example.test","url":"two.example.test"}]`)
	stderr := w.mustSearch()
	if !strings.Contains(stderr, "several 1Password accounts") {
		t.Fatalf("no ambiguity notice: %q", stderr)
	}
	if _, stderr, err := w.run("services", "auth", "cache", "account", "same@example.test", "--home", w.home); err == nil {
		t.Fatalf("ambiguous email accepted: %s", stderr)
	}
	if _, stderr, err := w.run("services", "auth", "cache", "account", "two.example.test", "--home", w.home); err != nil {
		t.Fatal(stderr)
	}
	reads := w.opReads()
	w.mustSearch()
	w.mustSearch()
	if n := w.opReads(); n != reads+1 {
		t.Fatalf("op reads = %d, want %d", n, reads+1)
	}
	data, _ := os.ReadFile(w.opLog)
	if !strings.Contains(string(data), "--account ACCT2") {
		t.Fatalf("not bound to the chosen account: %s", data)
	}
	writeCLITestFile(t, accounts, `[{"account_uuid":"ACCT1","email":"a@example.test"},{"account_uuid":"ACCT3","email":"c@example.test"}]`)
	if _, stderr, err := w.search(); err == nil || !strings.Contains(stderr, "no longer on this machine") {
		t.Fatalf("missing saved account: %v %s", err, stderr)
	}
}

func TestE2EServiceAccountStaysDirect(t *testing.T) {
	w := newE2EWorld(t)
	cmd := w.command("fleet", "search", "box", "--home", w.home, "--manifest", "acme")
	cmd.Env = append(cmd.Env, "OP_SERVICE_ACCOUNT_TOKEN=fake-sa")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	if w.killBroker() {
		t.Fatal("service account mode started a broker")
	}
}

func TestE2ENoSecretOnDiskArgvOrOutput(t *testing.T) {
	w := newE2EWorld(t)
	stderr := w.mustSearch()
	status, _, _ := w.run("services", "auth", "status", "registry", "--home", w.home)
	jsonStatus, _, _ := w.run("services", "auth", "cache", "status", "--home", w.home, "--json")
	for _, text := range []string{stderr, status, jsonStatus} {
		if strings.Contains(text, e2eKeyOne) {
			t.Fatalf("key in output: %s", text)
		}
	}
	if !strings.Contains(status, "live check") {
		t.Fatalf("status not labeled: %q", status)
	}
	err := filepath.Walk(w.home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Mode()&os.ModeSocket != 0 {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(e2eKeyOne)) {
			t.Errorf("key on disk in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only this test's broker: its arguments and environment.
	pids := brokerPIDs(t, w.bin, w.home)
	if len(pids) == 0 {
		t.Fatal("no broker to inspect")
	}
	for _, pid := range pids {
		out, err := exec.Command("ps", "eww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out, []byte(e2eKeyOne)) {
			t.Fatal("key visible in the broker's arguments or environment")
		}
	}
}

func TestE2ESetVerifiesBeforeCaching(t *testing.T) {
	w := newE2EWorld(t)
	w.mustSearch()
	w.current.Store("fake-service-wants-something-else")
	if _, _, err := w.run("services", "auth", "set", "registry", "--ref", "op://Private/Other/credential", "--home", w.home); err == nil {
		t.Fatal("rejected key saved")
	}
	w.current.Store(e2eKeyOne)
	reads := w.opReads()
	if _, stderr, err := w.run("services", "auth", "set", "registry", "--ref", "op://Private/Other/credential", "--home", w.home); err != nil {
		t.Fatal(stderr)
	}
	if n := w.opReads(); n != reads+1 {
		t.Fatalf("set reads = %d", n-reads)
	}
	status, _, _ := w.run("services", "auth", "cache", "--home", w.home)
	if strings.Contains(status, "registry:") {
		t.Fatalf("old or unverified key still cached: %q", status)
	}
	w.mustSearch()
	if _, stderr, err := w.run("services", "auth", "unset", "registry", "--home", w.home); err != nil {
		t.Fatal(stderr)
	}
	status, _, _ = w.run("services", "auth", "cache", "--home", w.home)
	if strings.Contains(status, "registry:") {
		t.Fatalf("unset left the key cached: %q", status)
	}
}

func TestE2ELongHomeFindsOneBrokerWhateverTMPDIR(t *testing.T) {
	w := newE2EWorld(t)
	long := filepath.Join(w.home, strings.Repeat("long-home-", 10))
	if err := os.MkdirAll(filepath.Dir(long), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(w.home, ".config"), filepath.Join(w.home, ".config-moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(w.home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(long, ".config", "my-cli", "manifests.json"), fmt.Sprintf(`{"version":1,"manifests":[{"name":"acme","git_url":"https://example.test/acme.git","local_path":%q}]}`, cache))
	writeCLITestFile(t, filepath.Join(long, ".my-cli", "workspaces", "handbook", "fleet", "README.md"), "# Fleet\n")
	t.Cleanup(func() {
		w.run("services", "auth", "lock", "--home", long)
		killBrokerFor(t, w.bin, long)
		// Only this home's directory: others may belong to real brokers.
		if dir, err := credcache.RuntimeDir(long); err == nil && strings.HasPrefix(dir, "/tmp/my-cred-") {
			os.RemoveAll(dir)
		}
	})
	for _, tmp := range []string{t.TempDir(), t.TempDir()} {
		cmd := w.command("fleet", "search", "box", "--home", long, "--manifest", "acme")
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	if n := w.opReads(); n != 1 {
		t.Fatalf("op reads = %d", n)
	}
}

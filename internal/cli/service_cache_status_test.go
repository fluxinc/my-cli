package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/credcache"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/serviceauth"
)

const twoOPAccounts = `[{"account_uuid":"PERSONAL","email":"me@example.test","url":"my.1password.com"},{"account_uuid":"WORK","email":"me@example.test","url":"acme.1password.com"}]`

// diagnosticSandbox makes any secret read, broker start or account call other
// than op account list fail the test, so a diagnosis can only look.
func diagnosticSandbox(t *testing.T, accounts string, listErr error) *[]string {
	t.Helper()
	for _, name := range []string{"OP_ACCOUNT", "OP_SERVICE_ACCOUNT_TOKEN", "OP_CONNECT_TOKEN", "MYCLI_NO_CRED_CACHE"} {
		t.Setenv(name, "")
	}
	previousRunner, previousSpawn, previousFlag, previousNotices := fleetAuthRunner, credBrokerSpawn, credNoCacheFlag, credNotices
	t.Cleanup(func() {
		fleetAuthRunner, credBrokerSpawn, credNoCacheFlag, credNotices = previousRunner, previousSpawn, previousFlag, previousNotices
	})
	credNoCacheFlag, credNotices = false, io.Discard
	// Doctor separately reports a missing op binary; give it a placeholder
	// so results do not depend on the host. fleetAuthRunner means it never runs.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "op"), []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var calls []string
	fleetAuthRunner = func(name string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, call)
		if call != "op account list --format=json" {
			t.Errorf("diagnosis ran %q; only op account list is allowed", call)
			return nil, errors.New("forbidden")
		}
		return []byte(accounts), listErr
	}
	credBrokerSpawn = func(string) error {
		t.Error("diagnosis started the credential broker")
		return errors.New("forbidden")
	}
	return &calls
}

func TestDecideCachePrecedence(t *testing.T) {
	if !credcache.Supported {
		t.Skip("cache unavailable on this platform")
	}
	one := `[{"account_uuid":"WORK","email":"me@example.test","url":"acme.1password.com"}]`
	cases := []struct {
		name     string
		accounts string
		listErr  error
		saved    credcache.Settings
		env      map[string]string
		flag     bool
		use      bool
		account  string
		source   string
		bypass   string
		reason   string
		err      bool
	}{
		{name: "flag", accounts: one, flag: true, bypass: "--no-cache"},
		{name: "env off", accounts: one, env: map[string]string{"MYCLI_NO_CRED_CACHE": "1"}, bypass: "MYCLI_NO_CRED_CACHE"},
		{name: "service account", accounts: one, env: map[string]string{"OP_SERVICE_ACCOUNT_TOKEN": "x"}, bypass: "OP_SERVICE_ACCOUNT_TOKEN"},
		{name: "connect", accounts: one, env: map[string]string{"OP_CONNECT_TOKEN": "x"}, bypass: "OP_CONNECT_TOKEN"},
		{name: "off", accounts: one, saved: credcache.Settings{Disabled: true}, bypass: "off"},
		{name: "single account", accounts: one, use: true, account: "WORK", source: "only account"},
		{name: "several unsaved", accounts: twoOPAccounts, reason: "several 1Password accounts"},
		{name: "saved", accounts: twoOPAccounts, saved: credcache.Settings{Account: "WORK"}, use: true, account: "WORK", source: "saved"},
		{name: "OP_ACCOUNT beats saved", accounts: twoOPAccounts, saved: credcache.Settings{Account: "WORK"}, env: map[string]string{"OP_ACCOUNT": "my.1password.com"}, use: true, account: "PERSONAL", source: "OP_ACCOUNT"},
		{name: "OP_ACCOUNT ambiguous email", accounts: twoOPAccounts, env: map[string]string{"OP_ACCOUNT": "me@example.test"}, reason: "OP_ACCOUNT does not name exactly one"},
		{name: "saved missing", accounts: `[{"account_uuid":"OTHER"}]`, saved: credcache.Settings{Account: "WORK"}, err: true},
		{name: "no accounts", accounts: `[]`, reason: "no 1Password account"},
		{name: "list fails", listErr: errors.New("op missing"), reason: "could not list"},
		{name: "list fails with saved", listErr: errors.New("op missing"), saved: credcache.Settings{Account: "WORK"}, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			diagnosticSandbox(t, tc.accounts, tc.listErr)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			credNoCacheFlag = tc.flag
			if err := credcache.SaveSettings(home, tc.saved); err != nil {
				t.Fatal(err)
			}
			d := decideCache(home)
			if d.Use != tc.use || d.Account.AccountUUID != tc.account || d.AccountSource != tc.source || d.Bypass != tc.bypass || (d.Err != nil) != tc.err {
				t.Fatalf("decision = %+v", d)
			}
			if !strings.Contains(d.Reason, tc.reason) {
				t.Fatalf("reason %q, want %q", d.Reason, tc.reason)
			}
			if !d.Use && d.Bypass == "" && d.Err == nil && d.Remedy == "" {
				t.Fatalf("unusable without a remedy: %+v", d)
			}
			// The diagnosis and the resolution path must agree.
			_, account, use, _, err := cachePlan(home, "op://Shared/item/credential")
			if use != d.Use || account != d.Account.AccountUUID || (err != nil) != (d.Err != nil) {
				t.Fatalf("cachePlan disagrees: use=%v account=%q err=%v, decision %+v", use, account, err, d)
			}
		})
	}
}

func TestCacheStatusReportsEffectiveStateWithoutFetching(t *testing.T) {
	if !credcache.Supported {
		t.Skip("cache unavailable on this platform")
	}
	home := t.TempDir()
	calls := diagnosticSandbox(t, twoOPAccounts, nil)
	var stdout, stderr strings.Builder
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "services", "auth", "cache", "status", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Several 1Password accounts are set up, so keys are not cached") || !strings.Contains(out, "my services auth cache account") {
		t.Fatalf("status hides the ambiguity:\n%s", out)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "services", "auth", "cache", "status", "--json", "--home", home}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Enabled   bool `json:"enabled"`
		Effective struct {
			Usable bool   `json:"usable"`
			Reason string `json:"reason"`
			Remedy string `json:"remedy"`
		} `json:"effective"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Effective.Usable || got.Effective.Reason == "" || got.Effective.Remedy == "" {
		t.Fatalf("json: %s", stdout.String())
	}
	if len(*calls) == 0 {
		t.Fatal("account list was never consulted")
	}

	// Choosing an account makes the cache usable; nothing is cached yet.
	if err := credcache.SaveSettings(home, credcache.Settings{Account: "WORK"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "services", "auth", "cache", "status", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out = stdout.String()
	if !strings.Contains(out, "Keys can be cached in 1Password account acme.1password.com (saved") || !strings.Contains(out, "Nothing is cached yet; the next eligible lookup may request 1Password approval") {
		t.Fatalf("cold usable cache misreported:\n%s", out)
	}
}

func TestDoctorReportsCacheEligibilityOffline(t *testing.T) {
	if !credcache.Supported {
		t.Skip("cache unavailable on this platform")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("doctor contacted the service: %s %s", r.Method, r.URL)
	}))
	defer server.Close()
	ref := manifest.Ref{Name: "acme", GitURL: "https://example.test/manifest.git"}
	opService := manifest.Service{ID: "registry", Kind: "http", AuthRef: "op://Shared/registry/credential", Connection: manifest.ServiceConnection{URL: server.URL}}
	envService := manifest.Service{ID: "tickets", Kind: "http", AuthRef: "env://TICKETS_TOKEN", Connection: manifest.ServiceConnection{URL: server.URL}}

	t.Run("ambiguous accounts warn with the remedy", func(t *testing.T) {
		home := t.TempDir()
		diagnosticSandbox(t, twoOPAccounts, nil)
		got := doctorPersonalServices(home, ref, []manifest.Service{opService}, lazyCacheDecision(home))
		if got[0].Status != "warning" || !strings.Contains(fmt.Sprint(got[0]), "my services auth cache account") {
			t.Fatalf("doctor: %+v", got[0])
		}
	})
	t.Run("usable cold cache is ok", func(t *testing.T) {
		home := t.TempDir()
		diagnosticSandbox(t, twoOPAccounts, nil)
		if err := credcache.SaveSettings(home, credcache.Settings{Account: "WORK"}); err != nil {
			t.Fatal(err)
		}
		got := doctorPersonalServices(home, ref, []manifest.Service{opService}, lazyCacheDecision(home))
		if got[0].Status != "ok" || !strings.Contains(fmt.Sprint(got[0]), "key cache: eligible, 1Password account acme.1password.com") {
			t.Fatalf("doctor: %+v", got[0])
		}
	})
	t.Run("missing saved account is an error", func(t *testing.T) {
		home := t.TempDir()
		diagnosticSandbox(t, `[{"account_uuid":"OTHER"}]`, nil)
		if err := credcache.SaveSettings(home, credcache.Settings{Account: "WORK"}); err != nil {
			t.Fatal(err)
		}
		got := doctorPersonalServices(home, ref, []manifest.Service{opService}, lazyCacheDecision(home))
		if got[0].Status != "error" {
			t.Fatalf("doctor: %+v", got[0])
		}
	})
	t.Run("user bypass stays ok", func(t *testing.T) {
		home := t.TempDir()
		diagnosticSandbox(t, twoOPAccounts, nil)
		t.Setenv("MYCLI_NO_CRED_CACHE", "1")
		got := doctorPersonalServices(home, ref, []manifest.Service{opService}, lazyCacheDecision(home))
		if got[0].Status != "ok" || !strings.Contains(fmt.Sprint(got[0]), "MYCLI_NO_CRED_CACHE") {
			t.Fatalf("doctor: %+v", got[0])
		}
	})
	t.Run("expired personal key is not cached", func(t *testing.T) {
		home := t.TempDir()
		diagnosticSandbox(t, twoOPAccounts, nil)
		if err := credcache.SaveSettings(home, credcache.Settings{Account: "WORK"}); err != nil {
			t.Fatal(err)
		}
		if err := serviceauth.Set(home, serviceBinding(ref, opService), serviceauth.Entry{Ref: "op://Private/registry/credential", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		got := doctorPersonalServices(home, ref, []manifest.Service{opService}, lazyCacheDecision(home))
		if detail := fmt.Sprint(got[0]); !strings.Contains(detail, "not eligible, the recorded key expiry has passed") || strings.Contains(detail, "eligible, 1Password account") {
			t.Fatalf("doctor: %+v", got[0])
		}
	})
	t.Run("missing op binary is still reported", func(t *testing.T) {
		home := t.TempDir()
		diagnosticSandbox(t, twoOPAccounts, nil)
		if err := credcache.SaveSettings(home, credcache.Settings{Account: "WORK"}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", t.TempDir())
		got := doctorPersonalServices(home, ref, []manifest.Service{opService}, lazyCacheDecision(home))
		if detail := fmt.Sprint(got[0]); got[0].Status != "warning" || !strings.Contains(detail, "op") || !strings.Contains(detail, "key cache: eligible") {
			t.Fatalf("doctor: %+v", got[0])
		}
	})
	t.Run("no op references never runs op", func(t *testing.T) {
		home := t.TempDir()
		calls := diagnosticSandbox(t, twoOPAccounts, nil)
		doctorPersonalServices(home, ref, []manifest.Service{envService}, lazyCacheDecision(home))
		if len(*calls) != 0 {
			t.Fatalf("op ran without an op:// reference: %v", *calls)
		}
	})
	t.Run("account list runs once per doctor", func(t *testing.T) {
		home := t.TempDir()
		calls := diagnosticSandbox(t, twoOPAccounts, nil)
		decide := lazyCacheDecision(home)
		other := opService
		other.ID = "billing"
		doctorPersonalServices(home, ref, []manifest.Service{opService, other}, decide)
		doctorPersonalServices(home, ref, []manifest.Service{opService}, decide)
		if len(*calls) != 1 {
			t.Fatalf("account list ran %d times", len(*calls))
		}
	})
}

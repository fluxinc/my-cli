package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/credcache"
	"github.com/fluxinc/my-cli/internal/serviceauth"
)

func TestExpiredCredentialKeepsSelectedAccount(t *testing.T) {
	if !credcache.Supported {
		t.Skip("cache account selection is unavailable")
	}
	home := t.TempDir()
	for _, name := range []string{"OP_ACCOUNT", "OP_SERVICE_ACCOUNT_TOKEN", "OP_CONNECT_TOKEN", "MYCLI_NO_CRED_CACHE"} {
		t.Setenv(name, "")
	}
	previousRunner, previousNotices, previousFlag := fleetAuthRunner, credNotices, credNoCacheFlag
	t.Cleanup(func() { fleetAuthRunner, credNotices, credNoCacheFlag = previousRunner, previousNotices, previousFlag })
	credNotices, credNoCacheFlag = io.Discard, false
	if err := credcache.SaveSettings(home, credcache.Settings{Account: "WORK"}); err != nil {
		t.Fatal(err)
	}
	var readArgs []string
	fleetAuthRunner = func(name string, args ...string) ([]byte, error) {
		if args[0] == "account" {
			return []byte(`[{"account_uuid":"PERSONAL"},{"account_uuid":"WORK"}]`), nil
		}
		readArgs = append([]string(nil), args...)
		return []byte("fake-key"), nil
	}
	token, cached, err := resolveServiceCredential(home, serviceauth.Binding{Service: "registry"}, "op://Shared/item/credential", time.Now().Add(-time.Hour))
	if err != nil || token != "fake-key" || cached != nil {
		t.Fatalf("unexpected direct result: cached=%v err=%v", cached != nil, err)
	}
	if !strings.Contains(strings.Join(readArgs, " "), "--account WORK") {
		t.Fatalf("selected account lost: %v", readArgs)
	}
}

func TestInvalidationReportsAccountConfigurationError(t *testing.T) {
	if !credcache.Supported {
		t.Skip("cache unavailable")
	}
	home := t.TempDir()
	for _, name := range []string{"OP_ACCOUNT", "OP_SERVICE_ACCOUNT_TOKEN", "OP_CONNECT_TOKEN", "MYCLI_NO_CRED_CACHE"} {
		t.Setenv(name, "")
	}
	previousRunner, previousFlag := fleetAuthRunner, credNoCacheFlag
	t.Cleanup(func() { fleetAuthRunner, credNoCacheFlag = previousRunner, previousFlag })
	credNoCacheFlag = false
	if err := credcache.SaveSettings(home, credcache.Settings{Account: "MISSING"}); err != nil {
		t.Fatal(err)
	}
	fleetAuthRunner = func(string, ...string) ([]byte, error) { return []byte(`[{"account_uuid":"OTHER"}]`), nil }
	if err := dropCachedServiceKey(home, serviceauth.Binding{Service: "registry"}, "op://Shared/item/credential"); err == nil {
		t.Fatal("failed account resolution reported successful invalidation")
	}
}

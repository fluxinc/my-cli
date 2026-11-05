package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fluxinc/my-cli/internal/credcache"
	"github.com/fluxinc/my-cli/internal/recordsvc"
	"github.com/fluxinc/my-cli/internal/serviceauth"
)

const (
	credBrokerOperation   = "broker"
	credentialCacheEscape = "rerun with --no-cache (or set MYCLI_NO_CRED_CACHE=1) to ask 1Password directly"
)

var (
	// credNoCacheFlag records --no-cache for this invocation.
	credNoCacheFlag bool
	// credBrokerSpawn starts the background broker; tests replace it.
	credBrokerSpawn = spawnCredentialBroker
	// credNotices receives cache notices; tests replace it.
	credNotices io.Writer = os.Stderr
)

// takeNoCacheFlag removes --no-cache from the commands that use service keys,
// wherever it appears, so each of their flag sets need not declare it.
func takeNoCacheFlag(args []string) []string {
	credNoCacheFlag = false
	if len(args) < 2 || (args[1] != "fleet" && args[1] != "support" && args[1] != "services") {
		return args
	}
	out := make([]string, 0, len(args))
	for i, arg := range args {
		if arg == "--" {
			return append(out, args[i:]...)
		}
		if arg == "--no-cache" {
			credNoCacheFlag = true
			continue
		}
		out = append(out, arg)
	}
	return out
}

func spawnCredentialBroker(home string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"services", "auth", credBrokerOperation}
	if home != "" {
		// The broker runs from another directory, so a relative home would
		// name a different registry and socket.
		if home, err = filepath.Abs(home); err != nil {
			return err
		}
		args = append(args, "--home", home)
	}
	cmd := exec.Command(executable, args...)
	// A bare environment: the broker needs no 1Password session or tokens.
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "MYCLI_NO_UPDATE_CHECK=1", "MYCLI_DISABLE_SELF_SKILL_SYNC=1"}
	cmd.Dir = string(filepath.Separator)
	credcache.Detach(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func newCredentialClient(home string) *credcache.Client {
	return &credcache.Client{Home: home, Spawn: credBrokerSpawn}
}

// opRunner is the command runner for op: the test stub if any, otherwise op
// with a bounded wait, so an unanswered prompt cannot hold a fetch forever.
func opRunner(name string, args ...string) ([]byte, error) {
	if fleetAuthRunner != nil {
		return fleetAuthRunner(name, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), credcache.ResolveTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

type opAccount struct {
	AccountUUID string `json:"account_uuid"`
	UserUUID    string `json:"user_uuid"`
	Email       string `json:"email"`
	URL         string `json:"url"`
	Shorthand   string `json:"shorthand"`
}

func listOPAccounts() ([]opAccount, error) {
	data, err := opRunner("op", "account", "list", "--format=json")
	if err != nil {
		return nil, err
	}
	var accounts []opAccount
	if err = json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// matchOPAccount accepts an account UUID, or a name exactly one account has:
// one email can sign in to both a personal and a work account.
func matchOPAccount(accounts []opAccount, want string) (opAccount, bool) {
	var matches []opAccount
	for _, account := range accounts {
		if account.AccountUUID != "" && account.AccountUUID == want {
			return account, true
		}
		for _, name := range []string{account.UserUUID, account.Email, account.URL, account.Shorthand} {
			if name != "" && strings.EqualFold(name, want) {
				matches = append(matches, account)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return opAccount{}, false
}

// resolveOPAccount finds the one account a cached key belongs to and returns
// its UUID, which is then passed to op read, so the key and the fetch cannot
// disagree about the account. It never guesses: an ambiguous choice returns a
// reason to stay uncached. `op account list` reads local metadata and does not
// ask for authorization.
func resolveOPAccount(settings credcache.Settings) (string, string, error) {
	accounts, err := listOPAccounts()
	if err != nil {
		if settings.Account != "" {
			return "", "", fmt.Errorf("could not confirm the saved 1Password account: %w", err)
		}
		return "", "could not list 1Password accounts", nil
	}
	if want := os.Getenv("OP_ACCOUNT"); want != "" {
		account, ok := matchOPAccount(accounts, want)
		if !ok || account.AccountUUID == "" {
			return "", "OP_ACCOUNT does not name exactly one 1Password account on this machine", nil
		}
		return account.AccountUUID, "", nil
	}
	if settings.Account != "" {
		// A saved choice must not quietly become whichever account op picks.
		account, ok := matchOPAccount(accounts, settings.Account)
		if !ok || account.AccountUUID == "" {
			return "", "", errors.New("the saved 1Password account is no longer on this machine; choose one with my services auth cache account <email>")
		}
		return account.AccountUUID, "", nil
	}
	if len(accounts) == 1 && accounts[0].AccountUUID != "" {
		return accounts[0].AccountUUID, "", nil
	}
	if len(accounts) == 0 {
		return "", "no 1Password account is set up on this machine", nil
	}
	return "", "several 1Password accounts are set up, so keys are not cached; choose one with my services auth cache account <email> or OP_ACCOUNT", nil
}

// cachePlan decides whether a reference uses the cache. A non-empty reason
// means the user expected caching and is not getting it, so it is reported.
func cachePlan(home, ref string) (settings credcache.Settings, account string, use bool, reason string, err error) {
	if !strings.HasPrefix(ref, "op://") {
		return settings, "", false, "", nil
	}
	// A service account or Connect server never prompts: nothing to save.
	if credNoCacheFlag || os.Getenv("MYCLI_NO_CRED_CACHE") != "" || os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "" || os.Getenv("OP_CONNECT_TOKEN") != "" {
		return settings, "", false, "", nil
	}
	if settings, err = credcache.LoadSettings(home); err != nil {
		return settings, "", false, "", fmt.Errorf("%w; fix or delete that file", err)
	}
	if settings.Disabled {
		return settings, "", false, "", nil
	}
	if !credcache.Supported {
		return settings, "", false, "the credential cache is not available on this platform yet", nil
	}
	account, reason, err = resolveOPAccount(settings)
	return settings, account, account != "", reason, err
}

func credentialKey(binding serviceauth.Binding, ref, account string) credcache.Key {
	return credcache.Key{ManifestName: binding.Name, Manifest: binding.Manifest, Service: binding.Service, URL: binding.URL, Ref: ref, Account: account}
}

// cachedCredential is a key held by the broker, and lets the record client
// replace it once when the service rejects it.
type cachedCredential struct {
	client   *credcache.Client
	request  credcache.Request
	settings credcache.Settings
	grant    credcache.Grant
}

// Refresh drops only the generation this command saw, so a newer key another
// command already fetched is reused rather than asked for again.
func (c *cachedCredential) Refresh() (string, error) {
	// Without the drop, Get would just return the rejected key.
	if err := c.Drop(); err != nil {
		return "", err
	}
	grant, err := c.client.Get(c.request, c.settings)
	if err != nil {
		return "", err
	}
	c.grant = grant
	noteCredentialFetch(c.request.Key.Service, grant)
	return grant.Token, nil
}

func (c *cachedCredential) Drop() error {
	if !c.grant.Cached {
		return nil
	}
	return c.client.Invalidate(c.request.Key, c.grant.Generation)
}

// resolveServiceCredential returns a service's key, through the cache when it
// applies. cached is nil in direct mode, which behaves exactly as before.
func resolveServiceCredential(home string, binding serviceauth.Binding, ref string, notAfter time.Time) (string, *cachedCredential, error) {
	settings, account, use, reason, err := cachePlan(home, ref)
	if err != nil {
		return "", nil, &credentialCacheFailure{err}
	}
	if use && !notAfter.IsZero() && !notAfter.After(time.Now()) {
		// The record may be out of date, since a renewed key can keep its
		// reference, so use the key as ever but do not cache past a known end.
		use, reason = false, "the recorded expiry of your "+binding.Service+" key has passed, so it is not cached (my services auth status rechecks it)"
	}
	if !use {
		if reason != "" {
			fmt.Fprintf(credNotices, "notice: %s; asking 1Password on each command\n", reason)
		}
		token, err := recordsvc.ResolveAuth(ref, fleetAuthRunner)
		return token, nil, err
	}
	cached := &cachedCredential{client: newCredentialClient(home), settings: settings, request: credcache.Request{
		Key:      credentialKey(binding, ref, account),
		NotAfter: notAfter,
		Resolve:  func() (string, error) { return resolveForAccount(ref, account) },
	}}
	cached.grant, err = cached.client.Get(cached.request, settings)
	if err != nil {
		var unavailable *credcache.UnavailableError
		var cooldown *credcache.CooldownError
		if errors.As(err, &unavailable) || errors.As(err, &cooldown) || !isHelperFailure(err) {
			return "", nil, &credentialCacheFailure{err}
		}
		return "", nil, err
	}
	noteCredentialFetch(binding.Service, cached.grant)
	return cached.grant.Token, cached, nil
}

// resolveForAccount reads a reference from one account, so the fetch cannot
// disagree with the account the key is filed under.
func resolveForAccount(ref, account string) (string, error) {
	return recordsvc.ResolveAuth(ref, func(name string, args ...string) ([]byte, error) {
		return opRunner(name, append([]string{args[0], "--account", account}, args[1:]...)...)
	})
}

// resolveCandidateCredential fetches a key that is not yet verified, so it
// stays out of the cache where another command could pick it up.
func resolveCandidateCredential(home, ref string) (string, error) {
	_, account, use, _, err := cachePlan(home, ref)
	if err != nil {
		return "", &credentialCacheFailure{err}
	}
	if use {
		return resolveForAccount(ref, account)
	}
	return recordsvc.ResolveAuth(ref, fleetAuthRunner)
}

// isHelperFailure tells ResolveAuth's own errors, which every one names the
// reference they failed on, from the cache's.
func isHelperFailure(err error) bool { return strings.HasPrefix(err.Error(), "auth_ref ") }

// credentialCacheFailure marks an error the cache caused rather than the key.
type credentialCacheFailure struct{ err error }

func (e *credentialCacheFailure) Error() string { return e.err.Error() }
func (e *credentialCacheFailure) Unwrap() error { return e.err }

func credentialCacheError(err error) bool {
	var failure *credentialCacheFailure
	return errors.As(err, &failure)
}

func noteCredentialFetch(service string, grant credcache.Grant) {
	switch {
	case grant.Fetched && grant.Cached:
		fmt.Fprintf(credNotices, "notice: %s key cached in memory until unused at %s, or %s at the latest. It stays usable after 1Password locks. Clear it with my services auth lock; turn caching off with my services auth cache off.\n",
			service, grant.IdleUntil.Local().Format("15:04"), grant.HardUntil.Local().Format("15:04"))
	case grant.Fetched:
		fmt.Fprintf(credNotices, "notice: %s key was fetched for this command only; the cache was locked or stopped meanwhile\n", service)
	}
}

// dropCachedServiceKey removes a key and fences any fetch of it under way.
// Without a known account there is no entry it could name; lifetimes bound it.
func dropCachedServiceKey(home string, binding serviceauth.Binding, ref string) error {
	_, account, use, _, err := cachePlan(home, ref)
	if err != nil || !use {
		return nil
	}
	return newCredentialClient(home).Invalidate(credentialKey(binding, ref, account), 0)
}

func (a app) warnCachedKeyKept(service string, err error) {
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: the old %s key may still be cached (%v); run my services auth lock\n", service, err)
	}
}

func (a app) runServicesAuthLock(args []string) error {
	var home string
	fs := newFlagSet("my services auth lock", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my services auth lock")
	}
	if err = newCredentialClient(home).Lock(); err != nil {
		return fmt.Errorf("could not reach the credential cache: %w", err)
	}
	fmt.Fprintln(a.stdout, "Credential cache cleared. Fetches under way cannot refill it.")
	return nil
}

func (a app) runServicesAuthCache(args []string) error {
	var home, idle, hard string
	var jsonOut bool
	fs := newFlagSet("my services auth cache", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&idle, "idle", "", "drop a key unused for this long")
	fs.StringVar(&hard, "max", "", "drop a key this long after fetching it")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "idle": true, "max": true})
	if err != nil {
		return err
	}
	operation := "status"
	if len(rest) > 0 {
		operation = rest[0]
	}
	if (operation == "account" && len(rest) != 2) || (operation != "account" && len(rest) > 1) {
		return fmt.Errorf("usage: my services auth cache status|on|off|account <email|url|uuid>")
	}
	settings, err := credcache.LoadSettings(home)
	if err != nil {
		return err
	}
	switch operation {
	case "status":
		if idle != "" || hard != "" {
			return fmt.Errorf("--idle and --max go with my services auth cache on")
		}
	case "on":
		if err = applyCacheLimits(&settings, idle, hard); err != nil {
			return err
		}
		settings.Disabled = false
		if err = credcache.SaveSettings(home, settings); err != nil {
			return err
		}
		// Keys already held were given the old limits.
		if idle != "" || hard != "" {
			if err = newCredentialClient(home).Lock(); err != nil {
				return fmt.Errorf("limits saved, but the cache could not be cleared: %w; rerun my services auth lock", err)
			}
		}
	case "off":
		settings.Disabled = true
		if err = credcache.SaveSettings(home, settings); err != nil {
			return err
		}
		if err = newCredentialClient(home).Lock(); err != nil {
			return fmt.Errorf("caching is off, but the cache could not be cleared: %w; rerun my services auth lock", err)
		}
	case "account":
		accounts, err := listOPAccounts()
		if err != nil {
			return fmt.Errorf("could not list 1Password accounts: %w", err)
		}
		account, ok := matchOPAccount(accounts, rest[1])
		if !ok || account.AccountUUID == "" {
			return fmt.Errorf("%q does not match a 1Password account on this machine; see op account list", rest[1])
		}
		settings.Account = account.AccountUUID
		if err = credcache.SaveSettings(home, settings); err != nil {
			return err
		}
	default:
		return fmt.Errorf("usage: my services auth cache status|on|off|account <email|url|uuid>")
	}
	entries, err := newCredentialClient(home).Status()
	if err != nil {
		return fmt.Errorf("could not reach the credential cache: %w", err)
	}
	idleLimit, hardLimit := settings.Limits()
	enabled := !settings.Disabled && credcache.Supported
	if jsonOut {
		if entries == nil {
			entries = []credcache.EntryStatus{}
		}
		return printJSON(a.stdout, map[string]any{"enabled": enabled, "supported": credcache.Supported, "idle": idleLimit.String(), "max": hardLimit.String(), "account": settings.Account, "entries": entries})
	}
	state := "on"
	switch {
	case !credcache.Supported:
		state = "not available on this platform; 1Password is asked on each command"
	case settings.Disabled:
		state = "off"
	}
	fmt.Fprintf(a.stdout, "Credential cache: %s (unused %s, %s at most).\n", state, idleLimit, hardLimit)
	for _, entry := range entries {
		fmt.Fprintf(a.stdout, "%s: cached until unused at %s, or %s at the latest\n", entry.Service, entry.IdleUntil.Local().Format("15:04"), entry.HardUntil.Local().Format("15:04"))
	}
	if len(entries) > 0 {
		fmt.Fprintln(a.stdout, "Cached keys stay usable after 1Password locks; my services auth lock clears them.")
	}
	return nil
}

func applyCacheLimits(settings *credcache.Settings, idle, hard string) error {
	candidate := *settings
	for _, limit := range []struct {
		value string
		field *string
	}{{idle, &candidate.Idle}, {hard, &candidate.Max}} {
		if limit.value == "" {
			continue
		}
		d, err := time.ParseDuration(limit.value)
		if err != nil || d <= 0 || d > credcache.MaxLifetime {
			return fmt.Errorf("cache limit %q must be a duration up to %s", limit.value, credcache.MaxLifetime)
		}
		*limit.field = limit.value
	}
	effectiveIdle, _ := time.ParseDuration(candidate.Idle)
	effectiveMax, _ := time.ParseDuration(candidate.Max)
	if effectiveIdle == 0 {
		effectiveIdle = credcache.DefaultIdle
	}
	if effectiveMax == 0 {
		effectiveMax = credcache.DefaultMax
	}
	if effectiveIdle > effectiveMax {
		return fmt.Errorf("--idle cannot exceed --max")
	}
	*settings = candidate
	return nil
}

// runServicesAuthBroker is the background process itself; my starts it.
func (a app) runServicesAuthBroker(args []string) error {
	var home string
	fs := newFlagSet("my services auth "+credBrokerOperation, a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	if _, err := parseInterspersed(fs, args, map[string]bool{"home": true}); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return credcache.RunBroker(ctx, home)
}

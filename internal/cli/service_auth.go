package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/recordsvc"
	"github.com/fluxinc/my-cli/internal/serviceauth"
)

func serviceBinding(ref manifest.Ref, s manifest.Service) serviceauth.Binding {
	return serviceauth.Binding{Name: ref.Name, Manifest: ref.GitURL, Service: s.ID, URL: s.Connection.URL}
}

func (a app) runServicesAuth(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(a.stdout, `Usage:
  my services auth set <service> --ref op://Private/item/credential [--manifest NAME] [--home DIR]
  my services auth status <service> [--manifest NAME] [--home DIR] [--json]
  my services auth unset <service> [--manifest NAME] [--home DIR]
  my services auth refresh <service> [--manifest NAME] [--home DIR] [--json]
  my services auth lock [--home DIR]
  my services auth cache status|on|off [--idle 1h] [--max 8h] [--home DIR] [--json]
  my services auth cache account <email|url|uuid> [--home DIR]

Create your personal key at the service's auth_setup_url, then save it in your
password manager. --ref accepts only op:// or env:// references, never a token.
Set verifies the credential with GET <connection.url>/me before saving your
reference. No shared manifest or password-manager item is changed. Unset returns
to the organization's auth_ref. Status verifies access live and reports key
expiry.

An op:// key is fetched once, then kept in memory by a background my process
for this user: until unused for 1h, or 8h at most. A cached key stays usable
after 1Password locks. Lock clears it and stops fetches under way; refresh
fetches it again; cache off disables caching; --no-cache or
MYCLI_NO_CRED_CACHE=1 skips it for one command. Cache status says whether keys
can be cached here (which 1Password account, or why not) and shows expiries;
it runs only op account list, never reads a key and makes no live check.
env:// references are never cached.`)
		return nil
	}
	operation := args[0]
	switch operation {
	case "lock":
		return a.runServicesAuthLock(args[1:])
	case "cache":
		return a.runServicesAuthCache(args[1:])
	case credBrokerOperation:
		return a.runServicesAuthBroker(args[1:])
	}
	if operation != "set" && operation != "status" && operation != "unset" && operation != "refresh" {
		return fmt.Errorf("unknown services auth operation; run my services auth --help")
	}
	var home, manifestName, authRef string
	var jsonOut bool
	fs := newFlagSet("my services auth "+operation, a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "registered manifest")
	fs.StringVar(&authRef, "ref", "", "personal op:// or env:// reference")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args[1:], map[string]bool{"home": true, "manifest": true, "ref": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("name exactly one service; run my services auth --help")
	}
	docs, err := loadRegisteredDocs(home, bindingManifestName(home, manifestName, ""))
	if err != nil {
		return err
	}
	type match struct {
		ref     manifest.Ref
		service manifest.Service
	}
	var matches []match
	for _, doc := range docs {
		for _, s := range doc.doc.Services {
			if s.ID == rest[0] {
				matches = append(matches, match{doc.ref, s})
			}
		}
	}
	if len(matches) != 1 {
		return fmt.Errorf("name a service that matches exactly one manifest; pass --manifest or run my services list")
	}
	ref, s := matches[0].ref, matches[0].service
	if s.Kind != "http" {
		return fmt.Errorf("personal auth references currently support HTTP record services only")
	}
	binding := serviceBinding(ref, s)
	existing, personal, err := serviceauth.Lookup(home, binding)
	if err != nil && operation == "status" {
		return err
	}
	if operation == "unset" {
		if err = serviceauth.Unset(home, binding); err != nil {
			return err
		}
		if personal {
			a.warnCachedKeyKept(s.ID, dropCachedServiceKey(home, binding, existing.Ref))
		}
		fmt.Fprintf(a.stdout, "Personal reference removed for %s; using the organization's auth_ref.\n", s.ID)
		return nil
	}
	effective := s.AuthRef
	if personal {
		effective = existing.Ref
	}
	if operation == "set" {
		if !serviceauth.ValidRef(authRef) {
			return fmt.Errorf("--ref must be an op:// or env:// reference; literal keys are never accepted")
		}
		effective = authRef
	}
	if operation == "refresh" {
		if err = dropCachedServiceKey(home, binding, effective); err != nil {
			return fmt.Errorf("could not clear the cached %s key: %w; %s", s.ID, err, credentialCacheEscape)
		}
	}
	var notAfter time.Time
	if personal {
		notAfter = existing.ExpiresAt
	}
	var token string
	var cached *cachedCredential
	if operation == "set" {
		token, err = resolveCandidateCredential(home, effective)
	} else {
		token, cached, err = resolveServiceCredential(home, binding, effective, notAfter)
	}
	if err != nil {
		if credentialCacheError(err) {
			return fmt.Errorf("service %s: %w; %s", s.ID, err, credentialCacheEscape)
		}
		return fmt.Errorf("service %s: %w; get a personal key at %s and run my services auth set %s --ref <reference>", s.ID, err, serviceSetupHint(s), s.ID)
	}
	client, err := recordsvc.NewClient(s.ID, s.Connection.URL, token)
	if err != nil {
		return err
	}
	if cached != nil {
		client.UseCredentialSource(cached)
	}
	ctx, cancel := serviceContext()
	defer cancel()
	identity, err := client.Identity(ctx)
	if err != nil {
		return fmt.Errorf("%w; get or renew your key at %s", err, serviceSetupHint(s))
	}
	if identity.Token.Name == "" || identity.Token.ExpiresAt.IsZero() || !identity.Token.ExpiresAt.After(time.Now()) {
		if cached != nil {
			_ = cached.Drop()
		}
		return fmt.Errorf("service %s /me did not return a usable key identity and expiry", s.ID)
	}
	if operation == "set" || personal {
		savedRef := effective
		if err = serviceauth.Set(home, binding, serviceauth.Entry{Ref: savedRef, ExpiresAt: identity.Token.ExpiresAt}); err != nil {
			return err
		}
		if operation == "set" {
			// Whatever was cached came from the reference this replaces, or
			// from an earlier version of the same one.
			previous := s.AuthRef
			if personal {
				previous = existing.Ref
			}
			a.warnCachedKeyKept(s.ID, dropCachedServiceKey(home, binding, previous))
			if previous != effective {
				a.warnCachedKeyKept(s.ID, dropCachedServiceKey(home, binding, effective))
			}
		}
		personal = true
	}
	writable := false
	for _, scope := range identity.Token.Scopes {
		if scope == "write" {
			writable = true
		}
	}
	if !writable {
		fmt.Fprintln(a.stderr, "notice: verified key is read-only; updates and comments need write scope")
	}
	if jsonOut {
		out := map[string]any{"service": s.ID, "personal": personal, "auth_ref": effective, "identity": identity, "live_check": true}
		if cached != nil {
			out["cache"] = map[string]any{"idle_until": cached.grant.IdleUntil, "hard_until": cached.grant.HardUntil, "fetched": cached.grant.Fetched}
		}
		return printJSON(a.stdout, out)
	}
	if !personal {
		fmt.Fprintln(a.stdout, "Using the organization auth_ref; set a personal reference for individual attribution.")
	}
	fmt.Fprintf(a.stdout, "%s: verified %s by live check; key %s expires %s\n", s.ID, strings.TrimSpace(identity.Issuer.Name), identity.Token.Name, identity.Token.ExpiresAt.Format(time.RFC3339))
	if cached != nil {
		now := time.Now()
		source := "from the cache"
		if cached.grant.Fetched {
			source = "fetched now"
		}
		fmt.Fprintf(a.stdout, "Cached key %s; held until unused at %s, or %s at the latest.\n", source, cacheExpiry(cached.grant.IdleUntil, now), cacheExpiry(cached.grant.HardUntil, now))
	}
	if operation == "set" {
		fmt.Fprintln(a.stdout, "Personal reference saved locally. The key itself was not stored.")
	}
	return nil
}
func serviceSetupHint(s manifest.Service) string {
	if s.AuthSetupURL != "" {
		return s.AuthSetupURL
	}
	return "the service's key settings (ask your organization admin for its setup URL)"
}

# Personal service access and comments

Status: shipped in v0.44.0.

HTTP record services can publish `auth_setup_url`. Onboarding introduces that
page and the personal-reference setup commands. `my services auth set/status/unset`
verify `/me`, save references rather than literal credentials, and bind an
override to the manifest Git URL, service ID and exact API URL. Fleet commands
prefer the override; doctor checks its environment/optional tools and cached
verified expiry offline. Key renewal is explicit and never rewrites shared
credentials or organization manifests.

`my fleet comment` posts plain text through a service's comment endpoint with a
caller-supplied idempotency key and freshly read revisions. The service owns
mention semantics, audit and notification delivery. No local Markdown write or
implicit publication occurs.

The mechanism remains organization neutral. Credential material is resolved
only at invocation and is scrubbed from diagnostics; no plaintext token store,
new secret prompt, Keychain integration or credential enrollment broker is
introduced.

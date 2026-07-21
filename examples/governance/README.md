# Governed GitHub repository template

Copy `my-governance.yml` to `.github/workflows/my-governance.yml` and merge the
relevant `CODEOWNERS` lines into `.github/CODEOWNERS` through an administrator-
approved pull request. Configure these repository variables:

- `MY_MANIFEST_REPOSITORY`: the private `owner/repository` containing the
  organization's manifest;
- `MY_MANIFEST_BRANCH`: its protected default branch; and
- `MY_GOVERNANCE_MOUNT`: the manifest mount id for this repository, or
  `@manifest` when protecting the manifest repository itself.
- `MY_CLI_COMMIT`: an audited 40-character commit id from `fluxinc/my-cli`.
- `MY_ATTESTATION_REPOSITORY`: the `owner/repository` declared by the
  manifest's `governance.attestations.mount`; and
- `MY_ATTESTATION_BRANCH`: that repository's protected default branch.

`MY_MANIFEST_READ_TOKEN` must be a read-only GitHub App or fine-grained token
that can read the private manifest repository. `MY_ATTESTATION_READ_TOKEN`
must likewise have read-only contents access to the authoritative attestation
repository. A single least-privilege GitHub App token may fill both secrets
when its installation is scoped to both repositories. Do not store a literal
token in the workflow or manifest.

Create an active default-branch ruleset with no bypass actors that requires:

- pull requests, one or more approvals, code-owner review, stale-approval
  dismissal, and resolved conversations;
- the `my-governance` check;
- no branch deletion; and
- no non-fast-forward updates.

Run `my governance audit --manifest <name> --json` after configuration. The
workflow uses `pull_request_target` so its definition comes from the trusted
base branch, checks out proposed bytes only as data, and passes the pull request
author's immutable numeric GitHub id to the validator. It never executes code
from the proposed checkout. It builds the validator only from the exact
`MY_CLI_COMMIT`; tags and moving branches are rejected.

The validator enforces universally required policies (those with an empty
`roles` list) for every pull-request author using only the fetched attestation
default-branch tip. A pull request that contains only the author's own current
attestation may bootstrap itself; bundling unrelated content does not. Policies
scoped to manifest roles remain local launch gates until the manifest has an
authoritative provider-identity-to-role mapping.

For every `governance.record_domains` entry with `"review":"codeowner"`, add
the domain path to CODEOWNERS (the sample includes `/decisions/`). The audit
checks this path coverage in addition to protecting the workflow and
CODEOWNERS file itself. `auto-pr` means automatic PR submission under these
rules; it never means direct push or automatic approval.

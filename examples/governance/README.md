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

`MY_MANIFEST_READ_TOKEN` must be a read-only GitHub App or fine-grained token
that can read the private manifest repository. Do not store a literal token in
the workflow or manifest.

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

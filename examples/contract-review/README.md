# Optional contract review in CI

Copy `review.yml` to the manifest repository's `.github/workflows/` after
reviewing it. The repository supplies `ANTHROPIC_API_KEY` as a secret and an
explicit `CONTRACT_REVIEW_MODEL` variable. Add read-only checkouts and
`--policy-root MOUNT=DIR` arguments for every declared policy mount; missing
sources produce `incomplete`, never a clean result. Supply credentials only to
trusted workflows; do not change this to `pull_request_target` to expose
secrets to forked PR code.

The sample uses a released my binary and a pinned Claude Code version, never
code from the candidate manifest. It comments advisory JSON only and does not
edit files, approve or merge. A fork without the secret cannot run a model
review. Organizations choose whether to install required checks; this example
does not make findings a merge gate.

The comment binds the PR head it reviewed. A later push or policy update makes
that evidence historical; rerun the workflow or `my admin contract review
--check` with the corresponding local evidence. CLI review is the primary
workflow; CI is optional.

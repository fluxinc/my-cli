#!/bin/bash
set -euo pipefail

case "${1:?scenario is required}" in
  *-arm64) scenario="${1%-arm64}"; evidence_arch="arm64" ;;
  *-amd64) scenario="${1%-amd64}"; evidence_arch="amd64" ;;
  *) scenario="$1"; evidence_arch="$(uname -m)" ;;
esac
export HOME="/tmp/home"
export SHELL="/bin/zsh"
export PATH="$HOME/.local/bin:/tmp/stubs:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
mkdir -p "$HOME/.local/bin" /tmp/stubs /tmp/evidence
EVENTS="/transcripts/${scenario}-${evidence_arch}-events.jsonl"
: >"$EVENTS"

record() {
  local name="$1"
  shift
  local output code
  set +e
  output="$({ "$@"; } 2>&1)"
  code=$?
  set -e
  jq -nc --arg scenario "$scenario" --arg step "$name" --arg output "$output" --argjson exit_code "$code" \
    '{scenario:$scenario,step:$step,exit_code:$exit_code,output:$output}' >>"$EVENTS"
  printf '%s\n' "$output" >&2
  return "$code"
}

make_release_fixture() {
  cp /candidate/my /tmp/my
  tar -C /tmp -czf /tmp/release.tar.gz my
  local digest
  digest="$(sha256sum /tmp/release.tar.gz | awk '{print $1}')"
  local release_arch
  case "$(uname -m)" in
    aarch64|arm64) release_arch=arm64 ;;
    x86_64) release_arch=amd64 ;;
    *) echo "unsupported fixture architecture" >&2; exit 2 ;;
  esac
  printf '%s  my-cli_0.0.0-container_linux_%s.tar.gz\n' "$digest" "$release_arch" >/tmp/checksums.txt
  cat >/tmp/stubs/curl <<'SH'
#!/bin/sh
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -w) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
case "$url" in
  *releases/latest) printf '%s' 'https://github.com/fluxinc/my-cli/releases/tag/v0.0.0-container' ;;
  *checksums.txt) cp /tmp/checksums.txt "$out" ;;
  *tar.gz) cp /tmp/release.tar.gz "$out" ;;
  *) exit 22 ;;
esac
SH
  chmod +x /tmp/stubs/curl
}

make_manifest_remote() {
  rm -rf /tmp/manifest-work /tmp/manifest.git
  mkdir -p /tmp/manifest-work
  git -C /tmp/manifest-work init -q -b master
  cat >/tmp/manifest-work/manifest.json <<'JSON'
{
  "manifest_version": 1,
  "organization": {"id": "acme", "name": "Acme Container Fixture"},
  "umbrella": {"recommended_path": "~/acme"},
  "tools": [
    {"id": "acme-required", "mode": "required", "purpose": "Required container fixture", "install": {"commands": ["install acme-required after explicit consent"]}},
    {"id": "acme-optional", "mode": "optional", "purpose": "Optional container fixture", "install": {"commands": ["install acme-optional only on request"]}}
  ],
  "roles": [
    {"id": "operator", "purpose": "Container fixture operator", "tools": ["acme-required"]}
  ]
}
JSON
  git -C /tmp/manifest-work add manifest.json
  git -C /tmp/manifest-work -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm init
  git clone -q --bare /tmp/manifest-work /tmp/manifest.git
}

make_codex() {
  cat >/tmp/stubs/codex <<'SH'
#!/bin/bash
set -euo pipefail
printf 'cwd=%s\nargs=%s\n' "$PWD" "$*" >>/tmp/evidence/harness.log
prompt="$*"
complete_join() {
  my setup --manifest acme --role operator --no-refresh --no-update-check
  my tools list --manifest acme --json >/tmp/evidence/tools-before.json
  jq -e '.[] | select(.tool.id == "acme-required" and .status == "missing")' /tmp/evidence/tools-before.json >/dev/null
  jq -e '.[] | select(.tool.id == "acme-optional" and .status == "missing")' /tmp/evidence/tools-before.json >/dev/null
  printf 'required-tool-consent=yes\nfirst-install-attempt=failed\n' >>/tmp/evidence/harness.log
  printf '#!/bin/sh\nexit 0\n' >"$HOME/.local/bin/acme-required"
  chmod +x "$HOME/.local/bin/acme-required"
  my tools list --manifest acme --json >/tmp/evidence/tools-after.json
  jq -e '.[] | select(.tool.id == "acme-required" and .status == "present")' /tmp/evidence/tools-after.json >/dev/null
  jq -e '.[] | select(.tool.id == "acme-optional" and .status == "missing")' /tmp/evidence/tools-after.json >/dev/null
  my doctor --manifest acme --no-fetch --json >/tmp/evidence/doctor.json
  root="$(my root --manifest acme --no-refresh --no-update-check)"
  cd "$root"
  my ai --no-session --no-refresh --no-update-check codex -- --daily-smoke
  my onboarding --complete --manifest acme
  my onboarding --status --manifest acme
}
if [[ "$prompt" == *"Branch: JOIN_BOOTSTRAP"* || "$prompt" == *"Branch: JOIN by default"* ]]; then
  my manifests sync acme
  complete_join
elif [[ "$prompt" == *"Branch: AUTHOR"* ]]; then
  my init acme --name "Acme Container Fixture" --path "$HOME/acme/handbook" --umbrella "$HOME/acme" --setup
  my manifests validate acme
  my doctor --manifest acme --no-fetch --json >/tmp/evidence/doctor.json
  root="$(my root --manifest acme --no-refresh --no-update-check)"
  cd "$root"
  my ai --no-session --no-refresh --no-update-check codex -- --daily-smoke
  my onboarding --complete --manifest acme
elif [[ "$prompt" == *"Branch: JOIN_REPAIR"* ]]; then
  [[ "$prompt" == *"invalid JSON"* ]]
  printf 'repair-prompt-observed=yes\n' >>/tmp/evidence/harness.log
  broken="$HOME/.local/share/my-cli/manifests/acme"
  mv "$broken" "${broken}.invalid-backup"
  my manifests sync acme
  complete_join
fi
SH
  chmod +x /tmp/stubs/codex
  mkdir -p "$HOME/.codex"
  printf '{}\n' >"$HOME/.codex/auth.json"
}

make_gh() {
  cat >/tmp/stubs/gh <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>/tmp/evidence/gh.log
case "$*" in
  "auth token") printf '%s\n' test-token ;;
  "api user") printf '%s\n' '{"id":1,"node_id":"U_fixture","login":"fixture"}' ;;
  "api -i repos/acme/acme-manifest") printf 'HTTP/2.0 200 OK\r\n\r\n%s\n' '{"id":7,"node_id":"R_fixture","full_name":"acme/acme-manifest","private":true,"permissions":{"admin":false,"push":false,"pull":true}}' ;;
  "auth git-credential get") cat >/dev/null; printf 'username=x-access-token\npassword=test-token\n' ;;
  *) exit 1 ;;
esac
SH
  chmod +x /tmp/stubs/gh
}

install_join() {
  script -qec "sh /candidate/install.sh --manifest acme file:///tmp/manifest.git --harness codex" /dev/null
}

make_release_fixture
case "$scenario" in
  minimal-join|minimal-cached|minimal-governed)
    make_manifest_remote
    if [[ "$scenario" == minimal-governed ]]; then
     jq '. + {governance:{authorization:{provider:"github",manifest_repository:"acme/acme-manifest",admin_permission:"admin"}}}' /tmp/manifest-work/manifest.json >/tmp/m.json
     cp /tmp/m.json /tmp/manifest-work/manifest.json
     git -C /tmp/manifest-work add manifest.json
     git -C /tmp/manifest-work -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm governed
     git -C /tmp/manifest-work push -q /tmp/manifest.git master
    fi
    make_codex
    make_gh
    mv /tmp/stubs/gh /tmp/evidence/gh.adapter
    cp /candidate/my "$HOME/.local/bin/my"
    my manifests add acme https://github.com/acme/acme-manifest.git
    if [[ "$scenario" == minimal-cached || "$scenario" == minimal-governed ]]; then
      mkdir -p "$HOME/.local/share/my-cli/manifests/acme"
      git clone -q /tmp/manifest.git "$HOME/.local/share/my-cli/manifests/acme"
    fi
    mv /usr/bin/git /tmp/evidence/git.real
    mv /tmp/stubs/codex /tmp/evidence/codex.adapter
    cat >/tmp/stubs/codex <<'SH'
#!/bin/bash
set -eu
if [[ "$*" != *--daily-smoke* ]]; then
  if command -v git || command -v gh; then
    echo "bootstrap unexpectedly has Git or gh" >&2; exit 1
  fi
  printf 'HARNESS_ENTRY git=absent gh=absent stdin_tty=%s stdout_tty=%s\n' "$(test -t 0 && echo yes || echo no)" "$(test -t 1 && echo yes || echo no)" >>/tmp/evidence/entry.log
  printf '%s\n' "$*" >>/tmp/evidence/entry.log
  mv /tmp/evidence/git.real /usr/bin/git
  cp /tmp/evidence/gh.adapter /tmp/stubs/gh
fi
exec /tmp/evidence/codex.adapter "$@"
SH
    chmod +x /tmp/stubs/codex
    # Serve the stable wrapper and canonical script through the same offline curl adapter.
    # shellcheck disable=SC2016 # Variables belong to the generated adapter.
    sed -i '/case "$url" in/a\  https://my-cli.com/install.sh) cat /candidate/stable-install.sh ;;\n  *raw.githubusercontent.com*/install.sh) cp /candidate/install.sh "$out" ;;' /tmp/stubs/curl
    export GIT_CONFIG_COUNT=1
    export GIT_CONFIG_KEY_0=url.file:///tmp/manifest.git.insteadOf
    export GIT_CONFIG_VALUE_0=https://github.com/acme/acme-manifest.git
    record piped_bootstrap script -qec 'curl -fsSL https://my-cli.com/install.sh | sh -s -- --manifest acme https://github.com/acme/acme-manifest.git --harness codex' /dev/null
    cat /tmp/evidence/entry.log
    record final_status my onboarding --status --manifest acme
    ;;
  join)
    make_manifest_remote
    make_codex
    record one_url_join install_join
    record manifest_state my manifests list --json
    record completion my onboarding --status --manifest acme
    record root my root --manifest acme --no-refresh --no-update-check
    grep -q -- '--daily-smoke' /tmp/evidence/harness.log
    before="$(wc -l </tmp/evidence/harness.log)"
    record completed_update install_join
    after="$(wc -l </tmp/evidence/harness.log)"
    [[ "$before" == "$after" ]]
    ;;
  author)
    make_codex
    record one_url_author script -qec "sh /candidate/install.sh --harness codex" /dev/null
    record completion my onboarding --status --manifest acme
    grep -q 'Branch: AUTHOR' /tmp/evidence/harness.log
    ;;
  resume)
    make_manifest_remote
    record first_without_harness install_join
    grep -q 'After resolving the message above, run: my onboarding --manifest acme' "$EVENTS"
    make_codex
    record same_url_resume install_join
    record completion my onboarding --status --manifest acme
    [[ "$(grep -c 'export PATH=' "$HOME/.zshrc")" -eq 1 ]]
    ;;
  missing-git)
    make_manifest_remote
    make_codex
    mv /usr/bin/git /usr/bin/git.real
    record first_without_git install_join
    grep -q '\[todo\] git is required' "$EVENTS"
    grep -q 'After resolving the message above, run: my onboarding --manifest acme' "$EVENTS"
    mv /usr/bin/git.real /usr/bin/git
    record same_url_after_git install_join
    record completion my onboarding --status --manifest acme
    ;;
  https-auth)
    make_manifest_remote
    make_codex
    record first_without_gh script -qec "sh /candidate/install.sh --manifest acme https://github.com/acme/acme-manifest.git --harness codex" /dev/null
    grep -q 'gh is not installed' "$EVENTS"
    grep -q 'gh auth login' "$EVENTS"
    make_gh
    printf '[user]\n\tname = Fixture\n' >"$HOME/.gitconfig"
    before="$(sha256sum "$HOME/.gitconfig")"
    export GIT_CONFIG_COUNT=1
    export GIT_CONFIG_KEY_0=url.file:///tmp/manifest.git.insteadOf
    export GIT_CONFIG_VALUE_0=https://github.com/acme/acme-manifest.git
    record same_url_after_gh script -qec "sh /candidate/install.sh --manifest acme https://github.com/acme/acme-manifest.git --harness codex" /dev/null
    after="$(sha256sum "$HOME/.gitconfig")"
    [[ "$before" == "$after" ]]
    grep -q 'api user' /tmp/evidence/gh.log
    grep -q 'api -i repos/acme/acme-manifest' /tmp/evidence/gh.log
    record completion my onboarding --status --manifest acme
    ;;
  path)
    export PATH="$HOME/.local/bin:$PATH"
    record inherited_path_install sh /candidate/install.sh --no-onboarding
    [[ "$(grep -c 'export PATH=' "$HOME/.zshrc")" -eq 1 ]]
    record new_login_shell zsh -lic 'command -v my'
    ;;
  conflict)
    make_manifest_remote
    cp /candidate/my "$HOME/.local/bin/my"
    my manifests add acme file:///tmp/original.git
    if record conflicting_url sh /candidate/install.sh --manifest acme file:///tmp/manifest.git --no-onboarding; then
      exit 1
    fi
    my manifests list --json | jq -e '.manifests[0].git_url == "file:///tmp/original.git"' >/dev/null
    ;;
  repair)
    make_manifest_remote
    make_codex
    cp /candidate/my "$HOME/.local/bin/my"
    my manifests add acme file:///tmp/manifest.git
    mkdir -p "$HOME/.local/share/my-cli/manifests/acme"
    printf 'not json\n' >"$HOME/.local/share/my-cli/manifests/acme/manifest.json"
    record repair_handoff my onboarding --agent --harness codex --manifest acme
    grep -q 'repair-prompt-observed=yes' /tmp/evidence/harness.log
    [[ -f "$HOME/.local/share/my-cli/manifests/acme.invalid-backup/manifest.json" ]]
    record repair_completion my onboarding --status --manifest acme
    record repair_root my root --manifest acme --no-refresh --no-update-check
    grep -q -- '--daily-smoke' /tmp/evidence/harness.log
    ;;
  deterministic)
    make_manifest_remote
    record no_agent_install sh /candidate/install.sh --manifest acme file:///tmp/manifest.git --no-onboarding
    grep -q '\[ok\] git' "$EVENTS"
    record setup_self_heal my setup --manifest acme --no-refresh --no-update-check
    grep -q 'not synced yet; cloning' "$EVENTS"
    [[ -f "$HOME/acme/AGENTS.md" ]]
    record doctor_prereq my doctor --manifest acme --no-fetch
    grep -q 'prereq\\tgit\\tok' "$EVENTS"
    grep -q 'prereq\\tpath\\tok' "$EVENTS"
    record sync_after_setup my manifests sync acme
    record root my root --manifest acme --no-refresh --no-update-check
    rm -rf "$HOME/.local/share/my-cli/manifests/acme"
    record no_agent_walkthrough my onboarding --no-agent --manifest acme
    grep -q 'Umbrella: /tmp/home/acme' "$EVENTS"
    ;;
  wrapper)
    cat >/tmp/stubs/curl <<'SH'
#!/bin/sh
exit 22
SH
    chmod +x /tmp/stubs/curl
    if record wrapper_download_failure sh /candidate/stable-install.sh; then
      exit 1
    fi
    ;;
  *)
    echo "unknown scenario: $scenario" >&2
    exit 2
    ;;
esac

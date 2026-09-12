#!/bin/sh
set -eu

ROOT="$(cd -- "$(dirname "$0")/../.." && pwd)"
IMAGE="${MYCLI_ONBOARDING_IMAGE:-my-cli-onboarding-candidate}"
OUT="${MYCLI_ONBOARDING_OUT:-$ROOT/tests/onboarding/transcripts}"
SCENARIOS="${MYCLI_ONBOARDING_SCENARIOS:-join author resume missing-git https-auth path conflict repair deterministic wrapper minimal-join minimal-cached minimal-governed}"
PLATFORMS="${MYCLI_ONBOARDING_PLATFORMS:-linux/arm64 linux/amd64}"

mkdir -p "$OUT"
SUMMARY="$OUT/summary.json"
RUN_DATE="$(date -u +%F)"
printf '{\n  "run_date": "%s",\n  "results": [\n' "$RUN_DATE" >"$SUMMARY"
first_result=1

failed=0
for platform in $PLATFORMS; do
  arch="${platform#linux/}"
  platform_image="${IMAGE}-${arch}"
  docker build --platform "$platform" -f "$ROOT/tests/onboarding/Dockerfile" -t "$platform_image" "$ROOT"
  for scenario in $SCENARIOS; do
    log="$OUT/${scenario}-${arch}.log"
    printf 'Running %s on %s...\n' "$scenario" "$platform"
    if docker run --rm --network none --platform "$platform" -v "$OUT:/transcripts" "$platform_image" "$scenario-$arch" >"$log" 2>&1; then
      printf '  PASS %s\n' "$log"
      status=pass
    else
      printf '  FAIL %s\n' "$log"
      status=fail
      failed=1
    fi
    if [ "$first_result" = "0" ]; then printf ',\n' >>"$SUMMARY"; fi
    first_result=0
    printf '    {"scenario":"%s","platform":"%s","status":"%s","events":"%s-%s-events.jsonl"}' \
      "$scenario" "$platform" "$status" "$scenario" "$arch" >>"$SUMMARY"
  done
done

printf '\n  ]\n}\n' >>"$SUMMARY"

exit "$failed"

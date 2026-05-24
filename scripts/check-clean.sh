#!/usr/bin/env bash
# Fails if legacy Elasticsearch-direct artifacts reappear in the repo.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if ! command -v rg >/dev/null 2>&1; then
  echo "check-clean: ripgrep (rg) is required" >&2
  exit 1
fi

FORBIDDEN=(
  'ELASTICSEARCH_HOST'
  'ELASTICSEARCH_API_KEY'
  'KIBANA_CLI_TOKEN'
  'internal/api'
  'index list'
  'docker-compose.e2e'
  'scripts/e2e-up.ps1'
  'scripts/e2e-seed.ps1'
  'scripts/e2e.local'
  'integration/cmd_e2e'
  'docs/INTEGRATION.md'
  'KIBANA_E2E_HOST'
  'localhost:9200'
  'es.example.com:9200'
)

failed=0
for pat in "${FORBIDDEN[@]}"; do
  if rg -n \
    --glob '!CHANGELOG.md' \
    --glob '!CONTRIBUTING.md' \
    --glob '!scripts/check-clean.ps1' \
    --glob '!scripts/check-clean.sh' \
    --glob '!docs/evidence/**' \
    "$pat" . >/tmp/kibana-cli-clean.txt 2>/dev/null; then
    echo "FORBIDDEN pattern '$pat':"
    head -8 /tmp/kibana-cli-clean.txt
    failed=1
  fi
done

if [[ $failed -ne 0 ]]; then
  echo
  echo "check-clean FAILED"
  exit 1
fi
echo "check-clean OK"

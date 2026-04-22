#!/usr/bin/env bash
# Installs a pre-commit hook that runs gofmt, vet, tests, and the test-gap check
# against the current working tree before allowing a commit.
#
# Run once after cloning: `make install-hooks` or `bash scripts/install-hooks.sh`.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

HOOK_PATH=".git/hooks/pre-commit"
mkdir -p .git/hooks

cat > "$HOOK_PATH" <<'HOOK'
#!/usr/bin/env bash
# notion-cli-go pre-commit hook: blocks commits that break format/vet/tests.
set -euo pipefail

# Only run on staged Go changes.
changed=$(git diff --cached --name-only --diff-filter=ACM | grep -E '\.go$' || true)
if [[ -z "$changed" ]]; then
  exit 0
fi

echo "→ gofmt check…"
out=$(gofmt -l $changed)
if [[ -n "$out" ]]; then
  echo "gofmt violations in staged files:"
  echo "$out"
  echo "Run: make fmt"
  exit 1
fi

echo "→ go vet…"
go vet ./...

echo "→ go test…"
go test ./...

echo "→ test-gap check…"
CHECK_TEST_GAPS_STRICT=0 bash scripts/check-test-coverage.sh

echo "pre-commit ✓"
HOOK

chmod +x "$HOOK_PATH"
echo "Installed $HOOK_PATH"

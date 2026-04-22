#!/usr/bin/env bash
# Flags exported Go functions that lack a matching Test* function.
#
# Usage:
#   scripts/check-test-coverage.sh                # warn-only (exit 0)
#   CHECK_TEST_GAPS_STRICT=1 scripts/check-test-coverage.sh   # exit 1 on gaps
#
# Scope: every .go file outside vendor/ and outside _test.go files.
# A function is considered "covered" if there is a Test<FuncName> or
# Test<FuncName>_<anything> declaration somewhere under the module.
#
# Intentional skips: add the function's fully-qualified name (pkg.Func)
# to scripts/test-coverage-skip.txt, one per line. Comments with # allowed.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SKIP_FILE="scripts/test-coverage-skip.txt"
STRICT="${CHECK_TEST_GAPS_STRICT:-0}"

# Collect exported function declarations outside tests and vendor.
# Shape: <pkg>\t<FuncName>\t<file>:<line>
exports=$(
  grep -rn --include='*.go' --exclude-dir=vendor -E '^func (\([^)]*\) )?[A-Z][A-Za-z0-9_]*\(' . \
    | grep -v '_test\.go:' \
    | awk -F: '{
        file=$1; line=$2;
        sub(/^\.\//, "", file);
        # Strip method receiver if present.
        sig=$3;
        sub(/^func \([^)]+\) /, "func ", sig);
        sub(/^func +/, "", sig);
        match(sig, /^[A-Za-z0-9_]+/);
        name=substr(sig, RSTART, RLENGTH);
        # Derive package as the parent directory (root => "main").
        n=split(file, parts, "/");
        pkg = (n > 1) ? parts[n-1] : "main";
        print pkg "\t" name "\t" file ":" line;
      }'
)

# Collect test function names across the module.
tests=$(
  grep -rhn --include='*_test.go' --exclude-dir=vendor -E '^func Test[A-Z][A-Za-z0-9_]*\(' . \
    | awk -F'func ' '{print $2}' \
    | awk -F'(' '{print $1}' \
    | sort -u
)

# Load skip list.
skip=""
if [[ -f "$SKIP_FILE" ]]; then
  skip=$(grep -vE '^\s*(#|$)' "$SKIP_FILE" || true)
fi

gaps=0
gap_lines=""
while IFS=$'\t' read -r pkg fn loc; do
  [[ -z "$fn" ]] && continue
  fq="${pkg}.${fn}"
  if echo "$skip" | grep -Fxq "$fq"; then
    continue
  fi
  # Match Test<FuncName> or Test<FuncName>_...
  if echo "$tests" | grep -Eq "^Test${fn}(\$|_)"; then
    continue
  fi
  gap_lines+="  ${fq}  (${loc})"$'\n'
  gaps=$((gaps + 1))
done <<< "$exports"

if [[ $gaps -eq 0 ]]; then
  echo "✓ Every exported function has a matching Test* (skips honored)."
  exit 0
fi

echo "Test coverage gaps — ${gaps} exported function(s) have no matching Test*:"
printf "%s" "$gap_lines"
echo
echo "To intentionally skip one, add its fully-qualified name (pkg.Func) to ${SKIP_FILE}."
if [[ "$STRICT" = "1" ]]; then
  exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

# Public repo audit — catches leaks before merge.
# Scans source paths only, excludes build artifacts.
# Patterns are constructed at runtime to avoid containing forbidden terms literally.

FAIL=0
SCAN_PATHS="internal/ cmd/ npm/ .github/ *.md"
EXCLUDE="--exclude-dir=.git --exclude-dir=dist --exclude=audit-public.sh"

check() {
  local label="$1"; shift
  if grep -rni $EXCLUDE "$@" $SCAN_PATHS 2>/dev/null; then
    echo "FAIL: $label"
    FAIL=1
  fi
}

echo "=== audit-public ==="

# 1. Forbidden terms — constructed to avoid literals in this file
P1=$(printf '\x6d\x79\x73\x74')        # provider name
P2=$(printf '\x74\x65\x71\x75\x69\x6c\x61\x70\x69')  # provider API
P3=$(printf '\x70\x69\x6c\x76\x79\x74\x69\x73')      # payment provider
check "forbidden terms" \
  -e "${P1}[^e]" -e "${P1}erium" -e "$P2" -e "$P3" -e 'resend\.com'

# 2. Denylisted imports (server-only packages)
check "server imports" \
  -e 'internal/server' -e 'internal/db' -e 'internal/gateway' \
  -e 'internal/billing' -e 'internal/monitor'

# 3. Old module path
check "old module path" -e 'shellroute/shellroute/' --include='*.go'

# 4. Secrets patterns
check "secrets" \
  -e 'sk_live' -e 'sk_test' -e 'pk_live' -e 'PRIVATE_KEY'

# 5. .env files
if ls .env* 2>/dev/null | grep -q .; then
  echo "FAIL: .env files found"
  FAIL=1
fi

# 6. LICENSE + NOTICE in npm packages
for dir in npm/shellroute npm/cli-*; do
  if [ -d "$dir" ]; then
    [ ! -f "$dir/LICENSE" ] && echo "FAIL: missing $dir/LICENSE" && FAIL=1
    [ ! -f "$dir/NOTICE" ] && echo "FAIL: missing $dir/NOTICE" && FAIL=1
  fi
done

if [ $FAIL -eq 0 ]; then
  echo "PASS"
else
  echo ""
  echo "Audit failed. Fix the issues above before merging."
  exit 1
fi

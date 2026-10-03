#!/usr/bin/env bash
# Regression tests for dry-run.sh.
#   tests.sh          offline only: runs nothing, no network, no API calls
#   tests.sh --live   also runs the --check cases. These call the shellroute API
#                     (no proxy sessions). Set SHELLROUTE_BIN=/path/to/shellroute
#                     to test a specific build; the "authenticated" case needs a
#                     logged-in machine.
set -uo pipefail
cd "$(dirname "$0")"
PASS=0; FAIL=0; API_CALLS=0
ok()  { echo "  PASS $1"; PASS=$((PASS+1)); }
bad() { echo "  FAIL $1"; shift; printf '%s\n' "$@" | sed 's/^/        /'; FAIL=$((FAIL+1)); }

REFUSED="refused: the command starts a non-HTTP tool"

# t LABEL WANT_EXIT MUST_CONTAIN -- args...
t() {
  local label="$1" want="$2" pat="$3"; shift 3; [ "${1:-}" = "--" ] && shift
  local out rc; out=$(bash ./dry-run.sh "$@" 2>&1); rc=$?
  if [ "$rc" = "$want" ] && printf '%s' "$out" | grep -qF -- "$pat"; then ok "$label"; else bad "$label (exit $rc, want $want, must contain: $pat)" "$out"; fi
}
# never LABEL SECRET -- args...   (output must not contain SECRET, whatever the exit code)
never() {
  local label="$1" secret="$2"; shift 2; [ "${1:-}" = "--" ] && shift
  local out; out=$(bash ./dry-run.sh "$@" 2>&1)
  if printf '%s' "$out" | grep -qF -- "$secret"; then bad "$label: leaked" "$out"; else ok "$label"; fi
}

echo "=== offline ==="
t "one quoted string with spaces accepted"   0 "country: DE" -- DE 'printf "%s\n" "hello world"'
t "separate words rejected"                  2 "one quoted string" -- DE printf "%s\n" "hello world"
t "single quote inside command accepted"     0 "country: JP" -- JP "echo it's quoted"
t "pipeline accepted"                        0 "country: US" -- US 'curl -sI https://a.example/ && curl -s https://b.example/ | grep -i x-region'
t "lowercase country normalized"             0 "country: GB" -- gb 'curl -sSI https://example.com/'
t "template printed with placeholder"        0 "bash -c '<command>'" -- US 'curl -sSI https://example.com/'
t "empty command rejected"                   2 "missing command" -- DE ''
t "whitespace command rejected"              2 "missing command" -- DE '   '
t "three-letter country rejected"            2 "invalid country code" -- USA 'curl https://example.com'
t "country injection rejected"               2 "invalid country code" -- 'US; rm -rf /' 'curl https://example.com'
t "no arguments rejected"                    2 "usage" --
t "ping refused"                             5 "$REFUSED" -- US 'ping example.com'
t "dig after a pipe refused"                 5 "$REFUSED" -- US 'curl -s https://example.com | dig example.com'
t "ssh refused"                              5 "$REFUSED" -- DE 'ssh host uptime'
t "env wrapper stripped: env ping refused"   5 "$REFUSED" -- US 'env ping example.com'
t "env -i wrapper stripped"                  5 "$REFUSED" -- US 'env -i ping example.com'
t "assignment stripped: FOO=1 ping refused"  5 "$REFUSED" -- US 'FOO=1 ping example.com'
t "sudo wrapper stripped: sudo dig refused"  5 "$REFUSED" -- US 'sudo dig example.com'
t "absolute path: /sbin/ping refused"        5 "$REFUSED" -- US '/sbin/ping example.com'
t "heuristic limit labeled"                  5 "heuristic check" -- US 'ping example.com'
t "timeout wrapper: timeout 5 ping refused"   5 "$REFUSED" -- US 'timeout 5 ping example.com'
t "timeout with unit: timeout 30s ping refused" 5 "$REFUSED" -- US 'timeout 30s ping example.com'
t "sudo -u user ping refused"                 5 "$REFUSED" -- US 'sudo -u root ping example.com'
t "nice -n 5 ping refused"                    5 "$REFUSED" -- US 'nice -n 5 ping example.com'
t "env -u VAR ping refused"                   5 "$REFUSED" -- US 'env -u FOO ping example.com'
t "xargs ping refused"                        5 "$REFUSED" -- US 'echo example.com | xargs ping'
t "uppercase PING refused"                    5 "$REFUSED" -- US 'PING example.com'
t "quoted-split pin'g' refused"               5 "$REFUSED" -- US "pin'g' example.com"
t "timeout 5 curl accepted"                   0 "country: US" -- US 'timeout 5 curl -sSI https://example.com/'
t "sudo -u root curl accepted"                0 "country: US" -- US 'sudo -u root curl -sSI https://example.com/'
t "playwright warned, not refused"           0 "Playwright does not read HTTP_PROXY" -- DE 'npx playwright test'
t "puppeteer warned, not refused"            0 "Puppeteer does not read HTTP_PROXY" -- DE 'node puppeteer-check.js'
never "no extracted names line at all"       "tools:" -- US 'curl -sI https://a.example/ | jq .price'
never "bearer token never printed"           "SYNTHETIC_TEST_ONLY" -- US "curl -H 'Authorization: Bearer SYNTHETIC_TEST_ONLY' https://api.example/"
never "--header form never printed"          "SYNTHETIC_TEST_ONLY" -- US 'curl --header "Authorization: Bearer SYNTHETIC_TEST_ONLY" https://api.example/'
never "cookie split by ; never printed"      "SYNTHETIC_TEST_ONLY" -- US 'curl -H "Cookie: foo; SYNTHETIC_TEST_ONLY" https://example.com/'
never "URL userinfo never printed"           "s3cr3t" -- US 'curl https://alice:s3cr3t@api.example/'
never "basic auth never printed"             "alice:pw" -- DE 'curl -u alice:pw https://api.example/'
never "token query param never printed"      "tok_123" -- DE 'curl "https://api.example/v1?x=1&token=tok_123"'
never "URL itself never printed"             "https://api.example" -- DE 'curl -sSI https://api.example/'
never "command text never printed on refusal" "example.com" -- US 'ping example.com'
never "tool name from refusal not echoed verbatim from input" "pingX" -- US 'pingX example.com'

if [ "${1:-}" = "--live" ]; then
  echo "=== live --check (API calls, no sessions) ==="
  if [ -n "${SHELLROUTE_BIN:-}" ]; then PATH="$(dirname "$SHELLROUTE_BIN"):$PATH"; fi
  if ! command -v shellroute >/dev/null 2>&1; then echo "  SKIP: no shellroute on PATH (set SHELLROUTE_BIN)"; else
    ver=$(shellroute --version 2>/dev/null | awk '{print $2}'); echo "  using shellroute ${ver:-?}"
    out=$(bash ./dry-run.sh --check US 'curl -s https://ipinfo.io/country' 2>&1); rc=$?; API_CALLS=$((API_CALLS+1))
    [ "$rc" = 0 ] && ok "authenticated machine: exit 0" || bad "authenticated machine (exit $rc)" "$out"
    out=$(SHELLROUTE_HOME=$(mktemp -d) bash ./dry-run.sh --check US 'curl -s https://ipinfo.io/country' 2>&1); rc=$?; API_CALLS=$((API_CALLS+1))
    [ "$rc" = 4 ] && ok "fresh machine, no key: exit 4" || bad "fresh machine, no key (exit $rc)" "$out"
    out=$(SHELLROUTE_HOME=$(mktemp -d) SHELLROUTE_API_KEY=pk_bogus bash ./dry-run.sh --check US 'curl -s https://ipinfo.io/country' 2>&1); rc=$?; API_CALLS=$((API_CALLS+1))
    if [ "$rc" = 4 ]; then
      if [ "$ver" = "0.1.5" ]; then printf '%s' "$out" | grep -q "hint:" && ok "bogus key on 0.1.5: exit 4 with login hint" || bad "bogus key on 0.1.5: hint missing" "$out"
      else printf '%s' "$out" | grep -q "hint:" && bad "bogus key on $ver: stale 0.1.5 hint shown" "$out" || ok "bogus key on $ver: exit 4, no 0.1.5 hint"; fi
    else bad "bogus key (exit $rc)" "$out"; fi
    out=$(PATH=/usr/bin:/bin bash ./dry-run.sh --check US 'curl -s https://example.com' 2>&1); rc=$?   # no API call: CLI absent
    [ "$rc" = 3 ] && ok "shellroute missing: exit 3 (no API call)" || bad "missing-CLI case (exit $rc)" "$out"
    echo "  API calls made: $API_CALLS"
  fi
fi

echo "=== results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]

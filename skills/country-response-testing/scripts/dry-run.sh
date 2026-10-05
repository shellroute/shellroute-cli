#!/usr/bin/env bash
# Validate a country code and a command before routing it through shellroute.
# Runs nothing. Never prints the command or anything extracted from it (it may
# carry credentials): output is fixed diagnostic text, the validated country and
# a wrapper template. The agent, which already has the authorized command,
# builds the final invocation itself.
#
# Usage: dry-run.sh [--check] COUNTRY 'COMMAND STRING'
#   COMMAND must be ONE argument (quote it). Separate words are rejected because
#   word boundaries and quotes would be lost.
#   --check   also verify the CLI is installed and authenticated. This makes one
#             API call (no proxy session, not metered).
#
# Exit codes: 0 ok | 2 invalid input | 3 shellroute not installed
#             4 not authenticated | 5 a tool the HTTP proxy cannot route
#
# The tool check is a heuristic, not a shell parser: it takes the first word of
# each segment split on | & ; after stripping common wrappers (env, sudo, time,
# nice, nohup, command, exec, setsid, stdbuf, timeout, xargs), their options and
# the arguments those options take (sudo -u NAME, nice -n N, timeout 5s, ...),
# and VAR=value assignments. The token is compared case-insensitively with shell
# quotes removed. Subshells (bash -c, sh -c), aliases, scripts and functions are
# not inspected. The agent must still look at what the command runs.
set -uo pipefail
set -f  # no globbing while word-splitting the command

check=0
if [ "${1:-}" = "--check" ]; then
  check=1
  shift
fi

if [ "$#" -ne 2 ]; then
  echo "usage: dry-run.sh [--check] COUNTRY 'COMMAND STRING' (got $# arguments; pass the command as one quoted string)" >&2
  exit 2
fi
country="$1"
cmd="$2"

if ! [[ "$country" =~ ^[A-Za-z]{2}$ ]]; then
  echo "invalid country code: '$country' (use a two-letter ISO code such as US, DE, GB)" >&2
  exit 2
fi
country=$(printf '%s' "$country" | tr '[:lower:]' '[:upper:]')

if [ -z "${cmd//[[:space:]]/}" ]; then
  echo "missing command: pass the exact request to reproduce as one quoted string" >&2
  exit 2
fi

# Tool name per segment.
skip2() { if [ "$#" -ge 2 ]; then n=2; else n=1; fi; }
tools=()
segments=$(printf '%s' "$cmd" | tr '|&;' '\n\n\n')
while IFS= read -r seg; do
  set -- $seg
  while [ "$#" -gt 0 ]; do
    case "$1" in
      sudo)    shift; while [ "$#" -gt 0 ]; do case "$1" in -u|-g|-C|-D|-h|-p|-r|-t|-T|-U|-R) skip2 "$@"; shift "$n" ;; -*) shift ;; *) break ;; esac; done ;;
      nice)    shift; while [ "$#" -gt 0 ]; do case "$1" in -n|--adjustment) skip2 "$@"; shift "$n" ;; -*) shift ;; *) break ;; esac; done ;;
      env)     shift; while [ "$#" -gt 0 ]; do case "$1" in -u|-C|-S|--unset|--chdir|--split-string) skip2 "$@"; shift "$n" ;; -*|*=*) shift ;; *) break ;; esac; done ;;
      xargs)   shift; while [ "$#" -gt 0 ]; do case "$1" in -I|-n|-P|-d|-L|-s|-E|-a) skip2 "$@"; shift "$n" ;; -*) shift ;; *) break ;; esac; done ;;
      timeout) shift; while [ "$#" -gt 0 ]; do case "$1" in -k|-s|--kill-after|--signal) skip2 "$@"; shift "$n" ;; -*) shift ;; *) break ;; esac; done
               [[ "${1:-}" =~ ^[0-9]+(\.[0-9]+)?[smhd]?$ ]] && shift ;;   # the duration
      time|nohup|command|exec|setsid|stdbuf) shift ;;
      -*) shift ;;      # option of the wrapper just skipped
      *=*) shift ;;     # VAR=value assignment
      *) break ;;
    esac
  done
  [ "$#" -eq 0 ] && continue
  t="${1##*/}"; t="${t//[\'\"]/}"; t=$(printf '%s' "$t" | tr '[:upper:]' '[:lower:]')
  tools+=("$t")
done <<< "$segments"

# Nothing extracted from the command is ever printed: quoted text can be split
# by the segment rule and would otherwise leak (e.g. a Cookie header).
for t in ${tools[@]+"${tools[@]}"}; do
  case "$t" in
    ping|ping6|dig|nslookup|host|traceroute|mtr|ssh|scp|sftp|nc|ncat|telnet)
      echo "refused: the command starts a non-HTTP tool (ping, dig, nslookup, host, traceroute, mtr, ssh, scp, sftp, nc, telnet), which the proxy does not route (heuristic check; inspect the command yourself)" >&2
      exit 5 ;;
  esac
done

# Browsers need explicit proxy configuration; say which kind. Matched on the
# command text, but nothing from it is printed.
case "$cmd" in
  *playwright*)
    echo "note: Playwright does not read HTTP_PROXY. Set proxy.server from process.env.HTTP_PROXY in playwright.config.ts:" >&2
    echo "      https://github.com/shellroute/playwright-country-routing-example" >&2 ;;
esac
case "$cmd" in
  *puppeteer*)
    echo "note: Puppeteer does not read HTTP_PROXY. Pass --proxy-server=\$HTTP_PROXY in the browser launch args." >&2 ;;
esac

if [ "$check" = 1 ]; then
  if ! command -v shellroute >/dev/null 2>&1; then
    echo "shellroute not installed: https://shellroute.com/docs/quickstart" >&2
    exit 3
  fi
  if ! shellroute balance --format json >/dev/null 2>&1; then
    echo "not authenticated: the user runs 'shellroute login' on this machine" >&2
    exit 4
  fi
fi

echo "country: $country"
echo "routed:  shellroute run --no-stat $country -- bash -c '<command>'   (build it yourself; nothing from the command is echoed here)"

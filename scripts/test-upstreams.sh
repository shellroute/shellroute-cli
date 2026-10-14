#!/usr/bin/env bash
# Layer 2 of the bring-your-own-proxy test plan: real proxy software in Docker.
#
# Starts 3proxy, gost, squid and an sshd container on one Docker network next
# to a marker server, builds the CLI, runs the check list below through each
# proxy, prints one table and exits non-zero on any FAIL. Nothing leaves the
# machine except image pulls. Requires: docker, python3, openssl, curl, go.
# ssh is optional. Works with bash 3.2 (macOS) and bash 5 (Linux CI).
#
#   C-01 upstream test reports the exit IP within 15 s
#   C-02 HTTPS via CONNECT: body received, exit 0, reached through the proxy
#   C-03 plain HTTP: same
#   C-04 wrong password: error names credentials, non-zero, < 15 s, no child
#   C-05 unreachable proxy: error names the cause, < 15 s
#   C-06 NO_PROXY bypass: a target listed in NO_PROXY is reached directly
#   C-07 20 MB download: sha256 matches, metered bytes within 2 percent
#   C-08 20 parallel curls all succeed
#   C-09 one connection held 90 s (UPSTREAMS_LONG=1; skipped otherwise)
#   C-10 child exit code 3 becomes shellroute exit code 3
#   C-14 credentials never leak: child env, upstream list, session file
#   C-11 (interactive shell) is covered by the Go shell tests, not here.
#
# How "reached through the proxy" is proven: the marker is a container that
# only the proxies can resolve, by the name "marker" on the shared network.
# The host cannot resolve it, so a request that arrives there went through a
# proxy, whatever source address the platform shows.
set -uo pipefail
cd "$(dirname "$0")/.."

TMP=$(mktemp -d)
HOME_DIR="$TMP/home"
mkdir -p "$HOME_DIR"
export SHELLROUTE_HOME="$HOME_DIR"
unset SHELLROUTE_API_KEY SHELLROUTE_PROXY
export SHELLROUTE_API_URL="http://127.0.0.1:1" # the API must never be needed
NET=sr-upstreams
CONTAINERS=(sr-marker sr-3proxy sr-gost sr-squid-open sr-squid-auth sr-sshd)
PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null; done
  docker rm -f "${CONTAINERS[@]}" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$TMP"
}
trap cleanup EXIT

say() { printf '%s\n' "$*" >&2; }
need() { command -v "$1" >/dev/null 2>&1 || { say "missing: $1"; exit 2; }; }
need docker; need python3; need openssl; need curl; need go
docker info >/dev/null 2>&1 || { say "docker daemon not running"; exit 2; }

say "== build =="
go build -o "$TMP/shellroute" ./cmd/shellroute || exit 2
SR="$TMP/shellroute"

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'; }
json_field() { python3 -c 'import sys,json
try: print(json.load(sys.stdin).get(sys.argv[1],""))
except Exception: print("")' "$1"; }
wait_port() { for _ in $(seq 1 80); do (echo > /dev/tcp/127.0.0.1/"$1") 2>/dev/null && return 0; sleep 0.5; done; return 1; }

# --- network and marker container ---
say "== start marker =="
docker rm -f "${CONTAINERS[@]}" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 2
MARK_PORT=$(free_port); MARK_TLS_PORT=$(free_port)
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 \
  -subj "/CN=marker" -addext "subjectAltName=DNS:marker,DNS:localhost,IP:127.0.0.1" \
  -keyout "$TMP/marker.key" -out "$TMP/marker.crt" >/dev/null 2>&1 || exit 2
chmod 644 "$TMP/marker.key" "$TMP/marker.crt"
docker run -d --name sr-marker --network "$NET" --network-alias marker \
  -p "127.0.0.1:$MARK_PORT:8080" -p "127.0.0.1:$MARK_TLS_PORT:8443" \
  -v "$PWD/scripts/upstreams/marker.py:/marker.py:ro" -v "$TMP/marker.crt:/certs/cert.pem:ro" -v "$TMP/marker.key:/certs/key.pem:ro" \
  python:3-alpine sh -c "python3 /marker.py 8080 & exec python3 /marker.py 8443 /certs/cert.pem /certs/key.pem" >/dev/null || exit 2
# A published port accepts connections before the container's process listens,
# so wait for an actual answer.
BLOB_SHA=""
for _ in $(seq 1 60); do
  BLOB_SHA=$(curl -s --noproxy '*' --max-time 2 "http://127.0.0.1:$MARK_PORT/" | json_field blob_sha256)
  [ -n "$BLOB_SHA" ] && break
  sleep 0.5
done
[ -n "$BLOB_SHA" ] || { say "marker did not answer"; docker logs sr-marker >&2; exit 2; }
for _ in $(seq 1 60); do
  curl -s --noproxy '*' --max-time 2 --cacert "$TMP/marker.crt" -o /dev/null "https://127.0.0.1:$MARK_TLS_PORT/" && break
  sleep 0.5
done
TARGET="marker:8080"
TARGET_TLS="marker:8443"
if curl -s --noproxy '*' --max-time 3 -o /dev/null "http://$TARGET/"; then
  say "the host resolves 'marker' itself; this run cannot prove traversal"; exit 2
fi

# A second marker runs on the host for the NO_PROXY check: reached directly it
# sees the host's own address; reached through a proxy container it does not.
HOST_MARK_PORT=$(free_port)
python3 scripts/upstreams/marker.py "$HOST_MARK_PORT" & PIDS+=($!)
LAN_IP=$(python3 -c 'import socket
s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
try:
    s.connect(("10.255.255.255", 1)); print(s.getsockname()[0])
except Exception: print("")')
case "$LAN_IP" in 127.*|"") LAN_IP="";; esac

# --- proxies ---
say "== start proxies =="
P3_HTTP=$(free_port); P3_SOCKS=$(free_port); P3_SOCKS_OPEN=$(free_port)
cat > "$TMP/3proxy.cfg" << CFG
nscache 65536
timeouts 1 5 30 60 180 1800 15 60
users alice:CL:s3cret
log
auth strong
allow alice
proxy -p3128
socks -p1080
flush
auth none
allow *
socks -p1081
CFG
docker run -d --name sr-3proxy --network "$NET" -p "127.0.0.1:$P3_HTTP:3128" -p "127.0.0.1:$P3_SOCKS:1080" -p "127.0.0.1:$P3_SOCKS_OPEN:1081" \
  -v "$TMP/3proxy.cfg:/etc/3proxy/3proxy.cfg:ro" 3proxy/3proxy:lts >/dev/null || exit 2

GOST_SOCKS=$(free_port); GOST_HTTPS=$(free_port)
docker run -d --name sr-gost --network "$NET" -p "127.0.0.1:$GOST_SOCKS:1082" -p "127.0.0.1:$GOST_HTTPS:8443" \
  -v "$TMP/marker.crt:/certs/cert.pem:ro" -v "$TMP/marker.key:/certs/key.pem:ro" gogost/gost:latest \
  -L "socks5://carol:pw@:1082" -L "https://bob:pw@:8443?certFile=/certs/cert.pem&keyFile=/certs/key.pem" >/dev/null || exit 2

SQUID_OPEN=$(free_port); SQUID_AUTH=$(free_port)
# squid drops to user "proxy" and cannot write /dev/stdout: keep logs off.
cat > "$TMP/squid-open.conf" << CFG
http_port 3129
acl localnet src all
http_access allow all
cache deny all
access_log none
pid_filename /tmp/squid.pid
CFG
printf 'dave:%s\n' "$(openssl passwd -apr1 pw)" > "$TMP/squid.passwd"
chmod 644 "$TMP/squid.passwd"
cat > "$TMP/squid-auth.conf" << CFG
http_port 3130
auth_param basic program /usr/lib/squid/basic_ncsa_auth /etc/squid/passwd
auth_param basic realm test
acl authed proxy_auth REQUIRED
http_access allow authed
http_access deny all
cache deny all
access_log none
pid_filename /tmp/squid.pid
CFG
docker run -d --name sr-squid-open --network "$NET" -p "127.0.0.1:$SQUID_OPEN:3129" \
  -v "$TMP/squid-open.conf:/etc/squid/squid.conf:ro" ubuntu/squid:6.6-24.04_edge >/dev/null || exit 2
docker run -d --name sr-squid-auth --network "$NET" -p "127.0.0.1:$SQUID_AUTH:3130" \
  -v "$TMP/squid-auth.conf:/etc/squid/squid.conf:ro" -v "$TMP/squid.passwd:/etc/squid/passwd:ro" ubuntu/squid:6.6-24.04_edge >/dev/null || exit 2

SSH_SOCKS=""
if command -v ssh >/dev/null 2>&1 && command -v ssh-keygen >/dev/null 2>&1; then
  SSH_PORT=$(free_port); SSH_SOCKS=$(free_port)
  ssh-keygen -q -t ed25519 -N "" -f "$TMP/sshkey" >/dev/null
  docker run -d --name sr-sshd --network "$NET" -p "127.0.0.1:$SSH_PORT:2222" -e PUID=1000 -e PGID=1000 \
    -e PUBLIC_KEY="$(cat "$TMP/sshkey.pub")" -e USER_NAME=tester linuxserver/openssh-server:latest >/dev/null || SSH_SOCKS=""
fi

for p in "$P3_HTTP" "$P3_SOCKS" "$P3_SOCKS_OPEN" "$GOST_SOCKS" "$GOST_HTTPS" "$SQUID_OPEN" "$SQUID_AUTH"; do
  wait_port "$p" || { say "port $p did not open"; docker logs --tail 20 sr-3proxy sr-gost sr-squid-open sr-squid-auth >&2 2>&1; exit 2; }
done
if [ -n "$SSH_SOCKS" ]; then
  if wait_port "$SSH_PORT"; then
    sleep 3
    # The image disables TCP forwarding by default; ssh -D needs it.
    docker exec sr-sshd sh -c 'f=/config/sshd/sshd_config; grep -q "^AllowTcpForwarding" "$f" && sed -i "s/^AllowTcpForwarding.*/AllowTcpForwarding yes/" "$f" || echo "AllowTcpForwarding yes" >> "$f"' >/dev/null 2>&1
    docker restart sr-sshd >/dev/null 2>&1
    wait_port "$SSH_PORT" && sleep 3
    ssh -i "$TMP/sshkey" -p "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
      -N -D "127.0.0.1:$SSH_SOCKS" tester@127.0.0.1 & PIDS+=($!)
    wait_port "$SSH_SOCKS" || SSH_SOCKS=""
  else
    SSH_SOCKS=""
  fi
fi

# --- check helpers (bash 3.2 compatible: results live in a TSV file) ---
RESULTS="$TMP/results.tsv"; : > "$RESULTS"
PROXIES=()
CHECKS=(C-01 C-02 C-03 C-04 C-05 C-06 C-07 C-08 C-09 C-10 C-14)
FAILS=0
mark() {
  printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$RESULTS"
  if [ "$3" = FAIL ]; then FAILS=$((FAILS+1)); say "  FAIL $1 $2: ${4:-}"; fi
  return 0
}
diag() { [ -n "${1:-}" ] && docker logs --tail 8 "$1" 2>&1 | sed 's/^/      | /' >&2; return 0; }
lookup() { awk -F'\t' -v p="$1" -v c="$2" '$1==p && $2==c {r=$3} END {print (r==""?"?":r)}' "$RESULTS"; }
timed() { local start; start=$(date +%s); "$@"; local rc=$?; echo "$(( $(date +%s) - start ))" > "$TMP/elapsed"; return $rc; }
# reached_marker <rc> <body>: the request got the marker's answer, which only a proxy can reach
reached_marker() { [ "$1" -eq 0 ] && echo "$2" | grep -q shellroute-upstream-test; }

run_checks() {
  local name="$1" url="$2" container="$3"
  PROXIES+=("$name")
  rm -f "$HOME_DIR/config.toml"
  "$SR" upstream add work "$url" >/dev/null 2>&1 || { for c in "${CHECKS[@]}"; do mark "$name" "$c" FAIL "upstream add failed"; done; return; }
  local out rc body src
  # C-01
  out=$(timed "$SR" upstream test work 2>&1); rc=$?
  if [ $rc -eq 0 ] && echo "$out" | grep -q "exit IP" && [ "$(cat "$TMP/elapsed")" -le 15 ]; then mark "$name" C-01 PASS; else mark "$name" C-01 FAIL "$out"; diag "$container"; fi
  # C-02 HTTPS via CONNECT
  body=$("$SR" run work --no-stat -- curl -sS --cacert "$TMP/marker.crt" "https://$TARGET_TLS/https" 2>"$TMP/err"); rc=$?
  if reached_marker "$rc" "$body"; then mark "$name" C-02 PASS; else mark "$name" C-02 FAIL "rc=$rc $(echo "$body" | head -c 120) $(tail -2 "$TMP/err")"; diag "$container"; fi
  # C-03 plain HTTP
  body=$("$SR" run work --no-stat -- curl -sS "http://$TARGET/plain" 2>"$TMP/err"); rc=$?
  if reached_marker "$rc" "$body"; then mark "$name" C-03 PASS; else mark "$name" C-03 FAIL "rc=$rc $(echo "$body" | head -c 120) $(tail -2 "$TMP/err")"; diag "$container"; fi
  # C-04 wrong password (skipped for proxies without auth)
  if [[ "$url" == *@* ]]; then
    local bad; bad=$(echo "$url" | sed -E 's#://([^:@]+):[^@]*@#://\1:WRONG@#')
    rm -f "$TMP/child-ran"
    out=$(timed "$SR" run --proxy "$bad" --no-stat -- touch "$TMP/child-ran" 2>&1); rc=$?
    if [ $rc -ne 0 ] && echo "$out" | grep -qiE "credentials|password" && [ ! -e "$TMP/child-ran" ] && [ "$(cat "$TMP/elapsed")" -le 15 ]; then mark "$name" C-04 PASS; else mark "$name" C-04 FAIL "rc=$rc $out"; fi
  else
    mark "$name" C-04 SKIP
  fi
  # C-05 unreachable proxy host (same scheme, closed port)
  local scheme=${url%%://*}
  out=$(timed "$SR" run --proxy "$scheme://127.0.0.1:1" --no-stat -- true 2>&1); rc=$?
  if [ $rc -ne 0 ] && echo "$out" | grep -qiE "refused|timeout|resolve" && [ "$(cat "$TMP/elapsed")" -le 15 ]; then mark "$name" C-05 PASS; else mark "$name" C-05 FAIL "rc=$rc $out"; fi
  # C-06 NO_PROXY bypass: the host marker on the LAN address, listed in NO_PROXY, is reached directly
  if [ -n "$LAN_IP" ]; then
    body=$(NO_PROXY="$LAN_IP" "$SR" run work --no-stat --no-check -- curl -sS --max-time 10 "http://$LAN_IP:$HOST_MARK_PORT/direct" 2>"$TMP/err"); rc=$?
    src=$(echo "$body" | json_field source)
    if [ $rc -eq 0 ] && [ "$src" = "$LAN_IP" ]; then mark "$name" C-06 PASS; else mark "$name" C-06 FAIL "rc=$rc src=$src want $LAN_IP"; fi
  else
    mark "$name" C-06 SKIP
  fi
  # C-07 20 MB download: sha256 and metered bytes
  "$SR" run work --no-check -- curl -sS -o "$TMP/blob" "http://$TARGET/blob" 2>"$TMP/err"; rc=$?
  local sha; sha=$(shasum -a 256 "$TMP/blob" 2>/dev/null | cut -d' ' -f1)
  local metered; metered=$(grep -oE "Data used:[^0-9]*[0-9.]+ [KMG]?B" "$TMP/err" | tail -1)
  local mb; mb=$(echo "$metered" | grep -oE "[0-9.]+ MB" | grep -oE "[0-9.]+")
  if [ $rc -eq 0 ] && [ "$sha" = "$BLOB_SHA" ] && [ -n "$mb" ] && python3 -c "import sys; mb=float('$mb'); sys.exit(0 if 19.6 <= mb <= 20.4 else 1)"; then mark "$name" C-07 PASS; else mark "$name" C-07 FAIL "rc=$rc sha_ok=$([ "$sha" = "$BLOB_SHA" ] && echo y || echo n) metered='$metered'"; fi
  # C-08 20 parallel curls
  out=$("$SR" run work --no-stat --no-check -- bash -c 'for i in $(seq 1 20); do curl -sS -f -o /dev/null "http://'"$TARGET"'/par$i" & done; wait; echo done' 2>&1); rc=$?
  if [ $rc -eq 0 ] && echo "$out" | grep -q done; then mark "$name" C-08 PASS; else mark "$name" C-08 FAIL "rc=$rc $out"; fi
  # C-09 long-lived connection
  if [ "${UPSTREAMS_LONG:-0}" = "1" ]; then
    out=$("$SR" run work --no-stat --no-check -- curl -sS -f -o /dev/null --limit-rate 220k --max-time 150 "http://$TARGET/blob" 2>&1); rc=$?
    if [ $rc -eq 0 ]; then mark "$name" C-09 PASS; else mark "$name" C-09 FAIL "rc=$rc $out"; fi
  else
    mark "$name" C-09 SKIP
  fi
  # C-10 child exit code
  "$SR" run work --no-stat --no-check -- sh -c 'exit 3' >/dev/null 2>&1; rc=$?
  if [ $rc -eq 3 ]; then mark "$name" C-10 PASS; else mark "$name" C-10 FAIL "rc=$rc"; fi
  # C-14 credentials never leak
  local pass=""; [[ "$url" == *@* ]] && pass=$(echo "$url" | sed -E 's#.*://[^:@]+:([^@]*)@.*#\1#')
  local leak=0
  out=$("$SR" run work --no-stat --no-check -- env 2>&1)
  echo "$out" | grep -E '^(HTTP|HTTPS|http|https)_(PROXY|proxy)=' | grep -qv '127.0.0.1' && leak=1
  [ -n "$pass" ] && echo "$out" | grep -q "$pass" && leak=1
  "$SR" upstream list 2>&1 | { [ -n "$pass" ] && grep -q "$pass"; } && leak=1
  "$SR" proxy --upstream work --no-check --format json >"$TMP/proxy.json" 2>/dev/null & local ppid=$!
  sleep 2
  cat "$HOME_DIR"/session-*.json 2>/dev/null | { [ -n "$pass" ] && grep -q "$pass"; } && leak=1
  # A background job of a non-interactive shell ignores SIGINT; SIGTERM stops the proxy.
  kill -TERM $ppid 2>/dev/null
  for _ in $(seq 1 20); do kill -0 $ppid 2>/dev/null || break; sleep 0.5; done
  kill -KILL $ppid 2>/dev/null; wait $ppid 2>/dev/null
  grep -q "proxy_url" "$TMP/proxy.json" || leak=1
  if [ $leak -eq 0 ]; then mark "$name" C-14 PASS; else mark "$name" C-14 FAIL "credential or env leak"; fi
}

say "== checks =="
[ -n "$LAN_IP" ] || say "  no LAN address found: C-06 is skipped"
run_checks "3proxy http+auth"    "http://alice:s3cret@127.0.0.1:$P3_HTTP" sr-3proxy
run_checks "3proxy socks5+auth"  "socks5://alice:s3cret@127.0.0.1:$P3_SOCKS" sr-3proxy
run_checks "3proxy socks5 open"  "socks5://127.0.0.1:$P3_SOCKS_OPEN" sr-3proxy
run_checks "gost socks5+auth"    "socks5h://carol:pw@127.0.0.1:$GOST_SOCKS" sr-gost
if [ "$(uname -s)" = "Linux" ]; then
  # Go honours SSL_CERT_FILE on Linux, so the proxy's self-signed cert can be trusted for the run.
  SSL_CERT_FILE="$TMP/marker.crt" run_checks "gost https+auth" "https://bob:pw@127.0.0.1:$GOST_HTTPS" sr-gost
else
  PROXIES+=("gost https+auth"); for c in "${CHECKS[@]}"; do mark "gost https+auth" "$c" SKIP; done
  say "  SKIP gost https+auth: Go on macOS ignores SSL_CERT_FILE, so a self-signed proxy certificate cannot be trusted for the run"
fi
run_checks "squid http open"     "http://127.0.0.1:$SQUID_OPEN" sr-squid-open
run_checks "squid http+auth"     "http://dave:pw@127.0.0.1:$SQUID_AUTH" sr-squid-auth
if [ -n "$SSH_SOCKS" ]; then
  run_checks "ssh -D socks5"     "socks5://127.0.0.1:$SSH_SOCKS" sr-sshd
else
  PROXIES+=("ssh -D socks5"); for c in "${CHECKS[@]}"; do mark "ssh -D socks5" "$c" SKIP; done
fi

# --- table ---
echo
printf '| %-18s |' "Proxy"; for c in "${CHECKS[@]}"; do printf ' %s |' "$c"; done; echo
printf '|%s|' "$(printf -- '-%.0s' $(seq 1 20))"; for c in "${CHECKS[@]}"; do printf '%s|' "------"; done; echo
for p in "${PROXIES[@]}"; do
  printf '| %-18s |' "$p"
  for c in "${CHECKS[@]}"; do printf ' %-4s |' "$(lookup "$p" "$c")"; done
  echo
done
echo
echo "$("$SR" --version 2>/dev/null | head -1) · $(uname -s) $(uname -m) · docker $(docker version --format '{{.Server.Version}}' 2>/dev/null) · $(date -u +%Y-%m-%d)"
echo "C-09 runs with UPSTREAMS_LONG=1. C-11 (interactive shell) is covered by the Go shell tests."
[ "$FAILS" -eq 0 ]

package tui

import (
	"fmt"
	"os"
)

func writeShellFunctions(f *os.File) {
	writeSSHProxyHelper(f)
	writeConnectFunc(f)
	writeDisconnectFunc(f)
	writeRotateFunc(f)
	writeSimpleCommands(f)
	writeHelpFunc(f)
	writeSSHFunc(f)
	writeSettingsCommands(f)
}

func writeSSHProxyHelper(f *os.File) {
	// Write HTTP CONNECT proxy helper for /ssh
	fmt.Fprint(f, `
_SR_PROXY_SCRIPT="/tmp/sr-proxy-$$.py"
cat > "$_SR_PROXY_SCRIPT" << 'PYEOF'
import sys, os, socket, selectors
h, p = sys.argv[1], sys.argv[2]
s = socket.create_connection(("127.0.0.1", int(os.environ["SHELLROUTE_PORT"])))
s.sendall(f"CONNECT {h}:{p} HTTP/1.1\r\nHost: {h}:{p}\r\n\r\n".encode())
buf = b""
while b"\r\n\r\n" not in buf:
    buf += s.recv(4096)
if b" 200 " not in buf.split(b"\r\n")[0]:
    sys.exit(1)
sel = selectors.DefaultSelector()
sel.register(s, selectors.EVENT_READ)
sel.register(sys.stdin.buffer, selectors.EVENT_READ)
while True:
    for key, _ in sel.select():
        if key.fileobj is s:
            d = s.recv(65536)
            if not d: sys.exit(0)
            sys.stdout.buffer.write(d)
            sys.stdout.buffer.flush()
        else:
            d = os.read(sys.stdin.fileno(), 65536)
            if not d: sys.exit(0)
            s.sendall(d)
PYEOF
chmod +x "$_SR_PROXY_SCRIPT"
export _SR_PROXY_CMD="python3 $_SR_PROXY_SCRIPT"
`)
}

func writeConnectFunc(f *os.File) {
	f.WriteString(`
/connect() {
  if [ -z "$1" ]; then
    echo -e "  \033[90mUsage: /connect <country code> or /connect <country code> <city name>\033[0m"
    echo -e "  \033[90mExample: /connect US  or  /connect US \"New York\"\033[0m"
    return 1
  fi
  local country city_arg iptype sticky_flag connect_msg
  country=$(echo "$1" | tr '[:lower:]' '[:upper:]' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  if [ ${#country} -ne 2 ]; then
    echo -e "  \033[90mCountry must be a 2-letter code (e.g. US, GB, DE).\033[0m"
    echo -e "  \033[90mUsage: /connect <country code> or /connect <country code> <city name>\033[0m"
    return 1
  fi
  [[ "$country" = "UK" ]] && country="GB"
  shift; city_arg="$*"
  iptype="${SHELLROUTE_IPTYPE:-residential}"
  sticky_flag="${SHELLROUTE_STICKY:-off}"
  # If already connected to same country+city, try rotate — if it fails, fall through to fresh connect
  if [ "$SHELLROUTE_COUNTRY" = "$country" ] && [ -n "$SHELLROUTE_SESSION_ID" ] && [ -z "$city_arg" -o "$city_arg" = "$SHELLROUTE_CITY" ]; then
    /rotate && return $?
    # Rotate failed (dead session, expired, etc.) — fall through to fresh connect
  fi
  if [ -n "$SHELLROUTE_SESSION_ID" ]; then
    echo "  Switching from ${SHELLROUTE_COUNTRY} to ${country}..."
  fi
  unset _SR_KILLSWITCH_SHOWN
  # Build connect message
  connect_msg="  Connecting to ${country}"
  [ -n "$city_arg" ] && connect_msg="${connect_msg}, ${city_arg}"
  connect_msg="${connect_msg} (${iptype})"
  if [ "$sticky_flag" = "on" ]; then
    connect_msg="${connect_msg} (sticky)"
  fi
  echo "${connect_msg}..."
  local url="http://127.0.0.1:${SHELLROUTE_CTRL}/connect?country=${country}&iptype=${iptype}"
  [ -n "$city_arg" ] && url="${url}&city=${city_arg}"
  [ "$sticky_flag" = "on" ] && url="${url}&sticky=1"
  local result
  result=$(curl -s --max-time 60 "${url}" 2>&1)
  local curl_rc=$?
  if echo "$result" | grep -q '^DISCONNECTED='; then
    local err=$(echo "$result" | grep '^ERROR ' | sed 's/^ERROR //')
    echo "  ✗ ${err}"
    unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
    unset SHELLROUTE_SESSION_ID SHELLROUTE_COUNTRY SHELLROUTE_EXIT_IP SHELLROUTE_PORT SHELLROUTE_COUNTRY_NAME SHELLROUTE_CITY
    return 1
  fi
  if [ $curl_rc -ne 0 ] || echo "$result" | grep -q "^ERROR "; then
    local err=$(echo "$result" | grep '^ERROR ' | sed 's/^ERROR //')
    if [ -z "$err" ]; then
      err="Connection timed out. Try again."
    fi
    echo "  ✗ ${err}"
    return 1
  fi
  local low_bal=$(echo "$result" | grep '^LOW_BALANCE=' | cut -d= -f2-)
  result=$(echo "$result" | grep -v '^LOW_BALANCE=')
  eval "$result"
  unset _SR_KILLSWITCH_SHOWN
  echo "  ✓ Connected to ${SHELLROUTE_COUNTRY} (${SHELLROUTE_COUNTRY_NAME:-$SHELLROUTE_COUNTRY}) — New IP: ${SHELLROUTE_EXIT_IP}"
  [ -n "$low_bal" ] && echo -e "  \033[33m⚠ Low balance: \$${low_bal} — top up at https://console.shellroute.com\033[0m"
}
`)
}

func writeDisconnectFunc(f *os.File) {
	f.WriteString(`
/disconnect() {
  if [ -z "$SHELLROUTE_SESSION_ID" ]; then
    echo "  Not connected."
    return 1
  fi
  local result
  result=$(curl -s "http://127.0.0.1:${SHELLROUTE_CTRL}/disconnect")
  if echo "$result" | head -1 | grep -q "^ERROR "; then
    local err=$(echo "$result" | sed 's/^ERROR //')
    echo "  ✗ ${err}"
    return 1
  fi
  unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
  unset SHELLROUTE_SESSION_ID SHELLROUTE_COUNTRY SHELLROUTE_EXIT_IP SHELLROUTE_PORT SHELLROUTE_COUNTRY_NAME SHELLROUTE_CITY
  unset _SR_KILLSWITCH_SHOWN _SR_HEALTH
  echo "$result"
  echo ""
}
`)
}

func writeRotateFunc(f *os.File) {
	f.WriteString(`
/rotate() {
  if [ -z "$SHELLROUTE_SESSION_ID" ]; then
    echo "  Not connected."
    return 1
  fi
  unset _SR_KILLSWITCH_SHOWN
  local iptype="${SHELLROUTE_IPTYPE:-residential}"
  local rotate_msg="  Rotating to ${SHELLROUTE_COUNTRY}"
  [ -n "$SHELLROUTE_CITY" ] && rotate_msg="${rotate_msg}, ${SHELLROUTE_CITY}"
  rotate_msg="${rotate_msg} (${iptype})"
  echo "${rotate_msg}..."
  local url="http://127.0.0.1:${SHELLROUTE_CTRL}/rotate?iptype=${iptype}&sticky=${SHELLROUTE_STICKY:-}"
  local result
  result=$(curl -s --max-time 35 "${url}" 2>&1)
  local curl_rc=$?
  if echo "$result" | grep -q '^DISCONNECTED='; then
    local err=$(echo "$result" | grep '^ERROR ' | sed 's/^ERROR //')
    echo "  ✗ ${err}"
    unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
    unset SHELLROUTE_SESSION_ID SHELLROUTE_COUNTRY SHELLROUTE_EXIT_IP SHELLROUTE_PORT SHELLROUTE_COUNTRY_NAME SHELLROUTE_CITY
    return 1
  fi
  if [ $curl_rc -ne 0 ] || echo "$result" | head -1 | grep -q "^ERROR "; then
    local err=$(echo "$result" | sed 's/^ERROR //')
    echo "  ✗ ${err}"
    return 1
  fi
  local no_other=$(echo "$result" | grep '^NO_OTHER_IPS=' | cut -d= -f2-)
  result=$(echo "$result" | grep -v '^NO_OTHER_IPS=')
  if [ -n "$no_other" ]; then
    echo -e "  \033[33m⚠ ${no_other}. Still connected.\033[0m"
    return 0
  fi
  eval "$result"
  echo "  ✓ Connected to ${SHELLROUTE_COUNTRY} (${SHELLROUTE_COUNTRY_NAME:-$SHELLROUTE_COUNTRY}) — New IP: ${SHELLROUTE_EXIT_IP}"
}
`)
}

func writeSimpleCommands(f *os.File) {
	f.WriteString(`
/status() {
  curl -sf "http://127.0.0.1:${SHELLROUTE_CTRL}/status"
}

/balance() {
  curl -sf "http://127.0.0.1:${SHELLROUTE_CTRL}/balance"
}

/countries() {
  curl -sf "http://127.0.0.1:${SHELLROUTE_CTRL}/countries"
}
`)
}

func writeHelpFunc(f *os.File) {
	f.WriteString(`
/help() {
  echo ""
  echo "  Session commands:"
  echo "  /connect <country> [city]   Connect and route traffic"
  echo "  /disconnect                 Disconnect current session"
  echo "  /rotate                     Rotate to a new IP"
  echo "  /status                     Show session info"
  echo "  /balance                    Show remaining credit balance"
  echo "  /countries                  List available countries"
  echo "  /cities <country>           List cities in a country"
  echo "  /iptype [residential|datacenter|mix]  Set/show IP type"
  echo "  /sticky [on|off]            Set/show sticky IP mode"
  echo "  /ssh <user@host>            SSH through the proxy"
  echo "  /login                      Switch account"
  echo "  /logout                     Remove credentials"
  echo "  /help                       Show this help"
  echo "  /exit                       Quit shellroute"
  echo ""
  echo "  Direct commands (from terminal, outside shellroute):"
  echo "  shellroute login                              Log in with email"
  echo "  shellroute logout                             Remove stored credentials"
  echo "  shellroute reveal-key                         Print stored API key"
  echo "  shellroute run <country> -- <cmd>             Run a command through proxy"
  echo "  shellroute run --no-stat <country> -- <cmd>   Run without session summary"
  echo "  shellroute connect --country <code>           Persistent proxy (Ctrl+C to stop)"
  echo "  shellroute balance                            Show credit balance"
  echo "  shellroute status                             Show session info"
  echo "  shellroute countries                          List available countries"
  echo "  shellroute cities <country>                   List cities in a country"
  echo ""
}
`)
}

func writeSSHFunc(f *os.File) {
	f.WriteString(`
/ssh() {
  if [ -z "$SHELLROUTE_PORT" ]; then
    echo "  Not connected. Run /connect first."
    return 1
  fi
  ssh -o ProxyCommand="$_SR_PROXY_CMD %h %p" "$@"
}
`)
}

func writeSettingsCommands(f *os.File) {
	f.WriteString(`
/iptype() {
  if [ -z "$1" ]; then
    echo "  IP type: ${SHELLROUTE_IPTYPE:-residential}"
    echo -e "  \033[90mOptions: residential | datacenter | mix\033[0m"
    return
  fi
  case "$1" in
    residential|datacenter|mix)
      export SHELLROUTE_IPTYPE="$1"
      echo "  IP type set to: $1"
      ;;
    *)
      echo -e "  \033[90mUsage: /iptype residential|datacenter|mix\033[0m"
      ;;
  esac
}

/sticky() {
  if [ -z "$1" ]; then
    echo "  Sticky: ${SHELLROUTE_STICKY:-off}"
    return
  fi
  case "$1" in
    on|off)
      export SHELLROUTE_STICKY="$1"
      echo "  Sticky set to: $1"
      ;;
    *)
      echo -e "  \033[90mUsage: /sticky on|off\033[0m"
      ;;
  esac
}

/cities() {
  if [ -z "$1" ]; then
    echo -e "  \033[90mUsage: /cities <country>  (e.g. /cities US)\033[0m"
    return 1
  fi
  local country
  country=$(echo "$1" | tr '[:lower:]' '[:upper:]')
  [[ "$country" = "UK" ]] && country="GB"
  curl -sf "http://127.0.0.1:${SHELLROUTE_CTRL}/countries?country=${country}"
}

/login() {
  echo "  Exiting to switch account..."
  builtin exit 2
}

/logout() {
  # Clear stored credentials
  local config_file="${HOME}/.shellroute/config.toml"
  if [ -f "$config_file" ]; then
    sed -i.bak 's/^api_key = .*/api_key = ""/' "$config_file" 2>/dev/null || \
    sed -i '' 's/^api_key = .*/api_key = ""/' "$config_file" 2>/dev/null
    rm -f "${config_file}.bak"
  fi
  echo "  ✓ Logged out. Credentials removed."
  builtin exit
}

/exit() {
  builtin exit
}`)
}

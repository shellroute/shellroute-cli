#!/usr/bin/env bash
set -uo pipefail

# Live signal-handling test for shellroute run.
# Creates one real (paid) session to verify SIGTERM forwarding and clean shutdown.
#
# Usage:
#   ./scripts/test-run-signal-live.sh --live [COUNTRY]
#
# Requirements:
#   - Authenticated shellroute (shellroute login or SHELLROUTE_API_KEY)
#   - Current checkout builds successfully
#
# Default country: US

usage() {
    echo "Usage: $0 --live [COUNTRY]"
    echo ""
    echo "Live signal-handling test for shellroute run."
    echo "Creates one real minimally-used paid session."
    echo ""
    echo "Options:"
    echo "  --live      Required. Confirms you accept one paid session."
    echo "  --help      Show this help."
    echo ""
    echo "Arguments:"
    echo "  COUNTRY     ISO country code (default: US)"
    exit 0
}

# --- Parse args ---
LIVE=false
COUNTRY=US
for arg in "$@"; do
    case "$arg" in
        --live) LIVE=true ;;
        --help|-h) usage ;;
        *) COUNTRY="$arg" ;;
    esac
done

if [ "$LIVE" != "true" ]; then
    echo "Error: this test creates a real paid session."
    echo "Run with --live to confirm: $0 --live [$COUNTRY]"
    exit 1
fi

# --- Build from current checkout ---
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR" "$READY_FILE" 2>/dev/null; [ -n "${SR_PID:-}" ] && kill "$SR_PID" 2>/dev/null; wait "$SR_PID" 2>/dev/null' EXIT

echo "=== Building shellroute from current checkout ==="
go build -o "$BUILD_DIR/shellroute" "$REPO_ROOT/cmd/shellroute" 2>&1
if [ $? -ne 0 ]; then
    echo "FAIL: build failed"
    exit 1
fi
SR="$BUILD_DIR/shellroute"
echo "Built: $SR"

# --- Verify auth ---
if ! "$SR" balance >/dev/null 2>&1; then
    echo "FAIL: not authenticated. Run shellroute login first."
    exit 1
fi
echo "Auth: ok"

# --- Create readiness signal file ---
READY_FILE=$(mktemp)
rm -f "$READY_FILE"

# --- Child script: signals readiness, traps SIGTERM, exits with code 42 ---
CHILD_SCRIPT='
ready_file="$1"
trap '"'"'echo CHILD_GOT_SIGTERM; exit 42'"'"' TERM
touch "$ready_file"
while true; do sleep 0.1; done
'

echo ""
echo "=== Running shellroute run $COUNTRY with signal test ==="

# Launch shellroute run in background with a child that traps SIGTERM
"$SR" run "$COUNTRY" -- bash -c "$CHILD_SCRIPT" -- "$READY_FILE" >"$BUILD_DIR/stdout" 2>"$BUILD_DIR/stderr" &
SR_PID=$!

# --- Wait for child readiness (max 60s) ---
echo "Waiting for child readiness..."
WAITED=0
while [ ! -f "$READY_FILE" ] && [ $WAITED -lt 60 ]; do
    sleep 1
    WAITED=$((WAITED + 1))
    # Check if shellroute already exited (connection failure)
    if ! kill -0 "$SR_PID" 2>/dev/null; then
        echo "FAIL: shellroute exited before child was ready."
        echo "--- stdout ---"
        cat "$BUILD_DIR/stdout"
        echo "--- stderr ---"
        cat "$BUILD_DIR/stderr"
        exit 1
    fi
done

if [ ! -f "$READY_FILE" ]; then
    echo "FAIL: child did not signal readiness within 60s."
    kill "$SR_PID" 2>/dev/null
    exit 1
fi
echo "Child ready after ${WAITED}s."

# --- Send SIGTERM to shellroute process ---
echo "Sending SIGTERM to shellroute (PID $SR_PID)..."
kill -TERM "$SR_PID"

# --- Wait for shellroute to exit (max 15s) ---
WAITED=0
while kill -0 "$SR_PID" 2>/dev/null && [ $WAITED -lt 15 ]; do
    sleep 1
    WAITED=$((WAITED + 1))
done

if kill -0 "$SR_PID" 2>/dev/null; then
    echo "FAIL: shellroute did not exit within 15s after SIGTERM."
    kill -9 "$SR_PID" 2>/dev/null
    exit 1
fi

wait "$SR_PID" 2>/dev/null
SR_EXIT=$?
SR_PID=""

echo "Shellroute exited (code $SR_EXIT) after ${WAITED}s."

# --- Verify results ---
PASS=0
FAIL=0

echo ""
echo "=== Verification ==="

# 1. Child received SIGTERM (printed CHILD_GOT_SIGTERM)
if grep -q "CHILD_GOT_SIGTERM" "$BUILD_DIR/stdout"; then
    echo "  PASS: child received SIGTERM"
    PASS=$((PASS + 1))
else
    echo "  FAIL: child did not receive SIGTERM"
    FAIL=$((FAIL + 1))
fi

# 2. Session ended cleanly (stderr contains "session ended")
if grep -q "session ended" "$BUILD_DIR/stderr"; then
    echo "  PASS: session ended cleanly"
    PASS=$((PASS + 1))
else
    echo "  FAIL: no 'session ended' in stderr"
    FAIL=$((FAIL + 1))
fi

# 3. No child process remains
if pgrep -f "CHILD_GOT_SIGTERM" >/dev/null 2>&1; then
    echo "  FAIL: child process still running"
    FAIL=$((FAIL + 1))
else
    echo "  PASS: no child process remains"
    PASS=$((PASS + 1))
fi

# 4. Shellroute exited (already verified above, but confirm non-hang)
echo "  PASS: shellroute exited within timeout"
PASS=$((PASS + 1))

echo ""
echo "=== Results ==="
echo "Passed: $PASS  Failed: $FAIL"

if [ $FAIL -gt 0 ]; then
    echo ""
    echo "--- stdout ---"
    cat "$BUILD_DIR/stdout"
    echo "--- stderr ---"
    cat "$BUILD_DIR/stderr"
    exit 1
fi

echo ""
echo "Signal handling verified: SIGTERM → child forwarded → session ended cleanly."

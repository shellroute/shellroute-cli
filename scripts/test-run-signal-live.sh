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
#   - Go toolchain (builds from current checkout)

SCRIPT_NAME="$(basename "$0")"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

usage() {
    cat <<EOF
Usage: $SCRIPT_NAME --live [COUNTRY]

Live signal-handling test for shellroute run.
Creates one real minimally-used paid session.

Options:
  --live      Required. Confirms you accept one paid session.
  --help, -h  Show this help.

Arguments:
  COUNTRY     ISO country code (default: US)
EOF
    exit 0
}

# --- Parse args strictly ---
LIVE=false
COUNTRY=US
POSITIONAL=0
while [ $# -gt 0 ]; do
    case "$1" in
        --live) LIVE=true ;;
        --help|-h) usage ;;
        -*)
            echo "Error: unknown flag: $1"
            echo "Run $SCRIPT_NAME --help for usage."
            exit 1
            ;;
        *)
            if [ $POSITIONAL -eq 0 ]; then
                COUNTRY="$1"
                POSITIONAL=1
            else
                echo "Error: unexpected argument: $1"
                echo "Run $SCRIPT_NAME --help for usage."
                exit 1
            fi
            ;;
    esac
    shift
done

if [ "$LIVE" != "true" ]; then
    echo "Error: this test creates a real paid session."
    echo "Run with --live to confirm: $SCRIPT_NAME --live [$COUNTRY]"
    exit 1
fi

# --- Create temp dir first (before any cleanup references) ---
WORK_DIR=$(mktemp -d) || { echo "FAIL: mktemp failed"; exit 1; }
[ -d "$WORK_DIR" ] || { echo "FAIL: temp dir does not exist"; exit 1; }
READY_FILE="$WORK_DIR/child-ready"
PGID_FILE="$WORK_DIR/child-pgid"
SR_PID=""

cleanup() {
    # Give shellroute time for its graceful 5s escalation + sess.Stop()
    if [ -n "$SR_PID" ] && kill -0 "$SR_PID" 2>/dev/null; then
        kill -TERM "$SR_PID" 2>/dev/null
        local w=0
        while kill -0 "$SR_PID" 2>/dev/null && [ $w -lt 7 ]; do
            sleep 1; w=$((w + 1))
        done
        kill -9 "$SR_PID" 2>/dev/null
        wait "$SR_PID" 2>/dev/null
    fi
    # Kill child process group by exact PGID
    if [ -f "$PGID_FILE" ]; then
        local pgid
        pgid=$(cat "$PGID_FILE" 2>/dev/null)
        if [ -n "$pgid" ] && [ "$pgid" -gt 0 ] 2>/dev/null && kill -0 -- "-$pgid" 2>/dev/null; then
            kill -9 -- "-$pgid" 2>/dev/null
        fi
    fi
    [ -d "$WORK_DIR" ] && rm -rf "$WORK_DIR"
}
trap cleanup EXIT

# --- Build from current checkout ---
echo "=== Building shellroute from current checkout ==="
(cd "$REPO_ROOT" && go build -o "$WORK_DIR/shellroute" ./cmd/shellroute) 2>&1
if [ $? -ne 0 ]; then
    echo "FAIL: build failed"
    exit 1
fi
SR="$WORK_DIR/shellroute"
echo "Built: $SR"

# --- Verify auth ---
if ! "$SR" balance >/dev/null 2>&1; then
    echo "FAIL: not authenticated. Run shellroute login first."
    exit 1
fi
echo "Auth: ok"

echo ""
echo "=== Running shellroute run $COUNTRY with signal test ==="

# --- Child script: writes PGID, signals readiness, traps SIGTERM, exits 0 ---
CHILD_SCRIPT='
pgid_file="$1"
ready_file="$2"
echo $$ > "$pgid_file"
trap '"'"'echo CHILD_GOT_SIGTERM >&2; exit 0'"'"' TERM
touch "$ready_file"
while true; do sleep 0.1; done
'

# Launch shellroute run in background
"$SR" run "$COUNTRY" -- bash -c "$CHILD_SCRIPT" -- "$PGID_FILE" "$READY_FILE" \
    >"$WORK_DIR/stdout" 2>"$WORK_DIR/stderr" &
SR_PID=$!

# --- Wait for child readiness (max 60s) ---
echo "Waiting for child readiness..."
WAITED=0
while [ ! -f "$READY_FILE" ] && [ $WAITED -lt 60 ]; do
    sleep 1
    WAITED=$((WAITED + 1))
    if ! kill -0 "$SR_PID" 2>/dev/null; then
        echo "FAIL: shellroute exited before child was ready."
        echo "--- stdout ---"
        cat "$WORK_DIR/stdout"
        echo "--- stderr ---"
        cat "$WORK_DIR/stderr"
        exit 1
    fi
done

if [ ! -f "$READY_FILE" ]; then
    echo "FAIL: child did not signal readiness within 60s."
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
    echo "--- stdout ---"
    cat "$WORK_DIR/stdout"
    echo "--- stderr ---"
    cat "$WORK_DIR/stderr"
    exit 1
fi

wait "$SR_PID" 2>/dev/null
SR_EXIT=$?
SR_PID=""

echo "Shellroute exited (code $SR_EXIT) after ${WAITED}s."

# --- Verify results ---
PASS=0
FAIL=0

show_output() {
    echo "--- stdout ---"
    cat "$WORK_DIR/stdout"
    echo "--- stderr ---"
    cat "$WORK_DIR/stderr"
}

echo ""
echo "=== Verification ==="

# 1. Shellroute exited 0 (child exited 0 from trap)
if [ "$SR_EXIT" -eq 0 ]; then
    echo "  PASS: shellroute exited 0"
    PASS=$((PASS + 1))
else
    echo "  FAIL: shellroute exited $SR_EXIT, want 0"
    FAIL=$((FAIL + 1))
    show_output
fi

# 2. Child received SIGTERM
if grep -q "CHILD_GOT_SIGTERM" "$WORK_DIR/stderr"; then
    echo "  PASS: child received SIGTERM"
    PASS=$((PASS + 1))
else
    echo "  FAIL: child did not print CHILD_GOT_SIGTERM"
    FAIL=$((FAIL + 1))
    show_output
fi

# 3. Session ended cleanly
if grep -qF "shellroute session ended." "$WORK_DIR/stderr"; then
    echo "  PASS: session ended cleanly"
    PASS=$((PASS + 1))
else
    echo "  FAIL: no 'shellroute session ended.' in stderr"
    FAIL=$((FAIL + 1))
    show_output
fi

# 4. No child process group remains (check exact PGID)
if [ -f "$PGID_FILE" ]; then
    CHILD_PGID=$(cat "$PGID_FILE")
    if [ -n "$CHILD_PGID" ] && [ "$CHILD_PGID" -gt 0 ] 2>/dev/null; then
        if kill -0 -- "-$CHILD_PGID" 2>/dev/null; then
            echo "  FAIL: child process group $CHILD_PGID still running"
            FAIL=$((FAIL + 1))
        else
            echo "  PASS: child process group $CHILD_PGID no longer exists"
            PASS=$((PASS + 1))
        fi
    else
        echo "  FAIL: invalid PGID in file: '$CHILD_PGID'"
        FAIL=$((FAIL + 1))
    fi
else
    echo "  FAIL: child did not write PGID file"
    FAIL=$((FAIL + 1))
fi

echo ""
echo "=== Results ==="
echo "Passed: $PASS  Failed: $FAIL"

if [ $FAIL -gt 0 ]; then
    exit 1
fi

echo ""
echo "Signal handling verified: SIGTERM → child forwarded → session ended cleanly."

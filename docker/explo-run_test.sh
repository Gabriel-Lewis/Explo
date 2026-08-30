#!/bin/sh
# Tests for explo-run.sh. Run it directly: sh docker/explo-run_test.sh
#
# There is no shell test harness in CI (the pipeline runs golangci-lint only),
# so this is here to be run by hand and to document what the lock guarantees.

set -u

script="$(dirname "$0")/explo-run.sh"
lockdir="$(mktemp -d)"
export EXPLO_LOCK_DIR="$lockdir"

failures=0

fail() {
    echo "FAIL: $1"
    failures=$((failures + 1))
}

pass() {
    echo "ok: $1"
}

# A job runs normally when nothing holds its lock.
out="$("$script" solo /bin/echo ran)"
if [ "$out" = "ran" ]; then
    pass "runs the command when the lock is free"
else
    fail "expected 'ran', got '$out'"
fi

# A second copy of the SAME job stands down instead of racing the first.
"$script" overlap /bin/sleep 2 &
first=$!
sleep 0.3

out="$("$script" overlap /bin/echo ran)"
case "$out" in
    *"skipping this schedule"*)
        pass "a second copy of the same job skips" ;;
    *)
        fail "expected the second run to skip, got '$out'" ;;
esac

# Skipping must look like success, or cron mails an error every schedule.
"$script" overlap /bin/echo ran >/dev/null
if [ $? -eq 0 ]; then
    pass "a skipped run exits 0"
else
    fail "a skipped run exited non-zero"
fi

# A DIFFERENT job must not be blocked: skipping another playlist entirely is
# worse than two runs overlapping.
out="$("$script" other /bin/echo ran)"
if [ "$out" = "ran" ]; then
    pass "a different job is not blocked"
else
    fail "a different job was blocked, got '$out'"
fi

wait "$first"

# Once the first finishes, the job can run again.
out="$("$script" overlap /bin/echo ran)"
if [ "$out" = "ran" ]; then
    pass "the lock is released when the run ends"
else
    fail "lock was not released, got '$out'"
fi

# The command's own exit status must reach cron unchanged.
"$script" status /bin/sh -c 'exit 3'
if [ $? -eq 3 ]; then
    pass "the command's exit status is passed through"
else
    fail "exit status was not passed through"
fi

rm -rf "$lockdir"

if [ "$failures" -ne 0 ]; then
    echo "$failures failure(s)"
    exit 1
fi
echo "all passed"

#!/bin/sh
# Run one scheduled Explo job under a lock of its own.
#
# crond starts a job on schedule whether or not the last one finished, and a run
# can outlast its own interval -- downloads are slow. Two copies of the same job
# then race on the same playlist. The lock makes the second one stand down.
#
# The lock is per job, not global: a long weekly-exploration must not cancel
# weekly-jams, since skipping a different playlist entirely is worse than
# running the two at once.
#
# Usage: explo-run.sh <job-name> <command> [args...]

set -u

if [ "$#" -lt 2 ]; then
    echo "[explo] usage: explo-run.sh <job-name> <command> [args...]" >&2
    exit 2
fi

job="$1"
shift

lock="${EXPLO_LOCK_DIR:-/tmp}/explo-${job}.lock"

# Without flock, run unlocked rather than not at all: an overlapping run is a
# lesser problem than a playlist that never gets built.
if ! command -v flock >/dev/null 2>&1; then
    echo "[explo] $job: flock unavailable, running without an overlap guard"
    exec "$@"
fi

# Hold the lock on a file descriptor so it is released when this shell exits,
# however it exits. Taking it this way also keeps the exit status unambiguous:
# flock fails here only because the lock is held, never because the job did.
exec 9>"$lock" || {
    echo "[explo] $job: couldn't open $lock, running without an overlap guard"
    exec "$@"
}

if ! flock -n 9; then
    echo "[explo] $job: previous run still in progress, skipping this schedule"
    exit 0
fi

exec "$@"

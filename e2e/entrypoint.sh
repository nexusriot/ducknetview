#!/usr/bin/env bash
# Runs the suite, then reports what the binary under test actually executed.
#
# The suite drives a separate process, so `go test -cover` sees none of it:
# the binary is built with -cover instead, every invocation drops its counters
# in GOCOVERDIR, and they are merged here. That is the only way the
# non-interactive modes and the live /proc and netlink readers show up as
# covered at all.
set -uo pipefail

mkdir -p "$GOCOVERDIR"

go test -tags e2e -count 1 -timeout 15m ./e2e/... "$@"
status=$?

echo
echo "==> coverage of the binary under test, from the e2e suite"
if compgen -G "$GOCOVERDIR/cov*" >/dev/null; then
    go tool covdata percent -i="$GOCOVERDIR" | sort
    echo
    go tool covdata func -i="$GOCOVERDIR" | tail -1
else
    echo "    (no coverage data: the binary was not built with -cover)"
fi

exit $status

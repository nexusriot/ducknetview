#!/usr/bin/env bash
# Build the image and run the e2e suite inside it. Everything the suite needs
# is in the container; nothing on the host is read or written.
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE="${DUCKNETVIEW_E2E_IMAGE:-ducknetview-e2e}"

echo "==> building $IMAGE"
docker build -q -f e2e/Dockerfile -t "$IMAGE" . >/dev/null

echo "==> running the e2e suite"
# --rm so nothing accumulates; no volumes, no ports, no host paths. The
# container keeps its own network namespace, which is what makes the socket
# assertions deterministic.
#
# CAP_SYS_ADMIN lets the namespace test create a network namespace inside the
# container and read sock_diag from it (setns), and CAP_NET_ADMIN lets it
# bring that namespace's loopback up. Both act on the container's own
# namespaces; without them that test skips.
exec docker run --rm --init --cap-add SYS_ADMIN --cap-add NET_ADMIN "$IMAGE" "$@"

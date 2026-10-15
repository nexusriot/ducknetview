# The e2e suite runs inside this image, not against it: the container is the
# host under test. Its network namespace is its own, so the set of interfaces,
# routes, listeners and connections is whatever the test puts there — which is
# what makes assertions about live /proc and netlink data possible at all.
FROM golang:1.24-bookworm

ENV CGO_ENABLED=0 \
    GOFLAGS=-mod=mod \
    GOTOOLCHAIN=local

WORKDIR /src

# Dependencies first, so editing the source does not re-download the module
# cache on every run.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Built once here rather than by the test, so a compile error is a build
# failure with a readable message instead of a test failure three layers down.
#
# -cover because the suite drives the binary as a separate process: without it
# everything the non-interactive modes and the live readers do is invisible to
# any coverage report.
RUN go build -cover -covermode=atomic -coverpkg=./... \
        -o /usr/local/bin/ducknetview ./cmd/ducknetview

# Nothing reaches the internet from the suite: the external-IP fetch is pointed
# at a closed port so it fails fast and deterministically, which is itself one
# of the paths worth covering.
ENV GOCOVERDIR=/tmp/covdata \
    DUCKNETVIEW_BIN=/usr/local/bin/ducknetview \
    HTTPS_PROXY=http://127.0.0.1:9 \
    HTTP_PROXY=http://127.0.0.1:9 \
    NO_COLOR=1

ENTRYPOINT ["/src/e2e/entrypoint.sh"]
CMD ["-v"]

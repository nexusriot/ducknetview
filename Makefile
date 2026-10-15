BINARY  := ducknetview
PKG     := ./cmd/ducknetview
VERSION := $(shell sed -n 's/.*Version = "\(.*\)".*/\1/p' internal/version/version.go)
PREFIX  ?= /usr/local

GO      ?= go
LDFLAGS := -s -w

.PHONY: all build run install uninstall fmt vet tidy test cover e2e e2e-clean clean deb deb-arm64

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

install: build
	install -Dm755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	install -Dm644 man/$(BINARY).1 $(DESTDIR)$(PREFIX)/share/man/man1/$(BINARY).1
	install -Dm644 completions/$(BINARY).bash $(DESTDIR)$(PREFIX)/share/bash-completion/completions/$(BINARY)
	install -Dm644 completions/_$(BINARY) $(DESTDIR)$(PREFIX)/share/zsh/site-functions/_$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	rm -f $(DESTDIR)$(PREFIX)/share/man/man1/$(BINARY).1
	rm -f $(DESTDIR)$(PREFIX)/share/bash-completion/completions/$(BINARY)
	rm -f $(DESTDIR)$(PREFIX)/share/zsh/site-functions/_$(BINARY)

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./...

cover:
	$(GO) test -coverprofile=coverage.out -coverpkg=./... ./...
	$(GO) tool cover -func=coverage.out | tail -1
	@echo "html: $(GO) tool cover -html=coverage.out"

# The e2e suite runs inside a container, because it needs a host whose
# interfaces, routes and sockets it controls. Nothing on this machine is read
# or written by it.
e2e:
	./e2e/run.sh

e2e-clean:
	-docker image rm ducknetview-e2e

deb:
	./build-deb.sh amd64

deb-arm64:
	./build-deb-arm64.sh

clean:
	rm -f $(BINARY) coverage.out
	rm -rf build

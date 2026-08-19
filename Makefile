BINARY  := ducknetview
PKG     := ./cmd/ducknetview
VERSION := $(shell sed -n 's/.*Version = "\(.*\)".*/\1/p' internal/version/version.go)
PREFIX  ?= /usr/local

GO      ?= go
LDFLAGS := -s -w

.PHONY: all build run install uninstall fmt vet tidy test clean deb deb-arm64

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

install: build
	install -Dm755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./...

deb:
	./build-deb.sh amd64

deb-arm64:
	./build-deb-arm64.sh

clean:
	rm -f $(BINARY)
	rm -rf build

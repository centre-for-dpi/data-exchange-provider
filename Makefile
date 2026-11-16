# Build, test and package the kit. Requires Go 1.27 and, for images, Docker.
VERSION ?= 0.1.0-dev
PKG     := github.com/centre-for-dpi/data-exchange-provider
LDFLAGS := -s -w -X $(PKG)/internal/buildinfo.Version=$(VERSION)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: build build-canal test

build:
	$(GOBUILD) -o bin/rid-node ./cmd/rid-node

build-canal:
	$(GOBUILD) -tags canal -o bin/rid-node-canal ./cmd/rid-node

test:
	go test ./...
	go test -tags canal ./...

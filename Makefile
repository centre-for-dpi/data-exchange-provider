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

.PHONY: fmt-check licenses vuln check

fmt-check:
	@files="$$(gofmt -l cmd internal tools)"; if [ -n "$$files" ]; then echo "gofmt: $$files"; exit 1; fi

# Rule 1 of CLAUDE.md: every module linked into either variant has an allowed license.
licenses:
	go run ./tools/licensecheck ./cmd/rid-node
	go run ./tools/licensecheck -tags canal ./cmd/rid-node

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

check: fmt-check
	go vet ./...
	go vet -tags canal ./...
	go mod verify
	$(MAKE) test licenses vuln

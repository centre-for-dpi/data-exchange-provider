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

.PHONY: image image-canal check-canal-deps

image:
	docker build -f deploy/Dockerfile --build-arg VARIANT=completo --build-arg VERSION=$(VERSION) -t rid-node:$(VERSION) .

image-canal:
	docker build -f deploy/Dockerfile --build-arg VARIANT=canal --build-arg VERSION=$(VERSION) -t rid-node:$(VERSION)-canal .

# The canal variant must not link the instant APIs module or any database driver (ADR-0001,
# install modes in ARCHITECTURE.md §1).
CANAL_FORBIDDEN := github.com/jackc/|github.com/go-sql-driver/|github.com/microsoft/go-mssqldb|github.com/sijms/go-ora|/internal/instant
check-canal-deps:
	@found="$$(go list -deps -tags canal ./cmd/rid-node | grep -E '$(CANAL_FORBIDDEN)')"; \
	if [ -n "$$found" ]; then echo "canal links forbidden packages:"; echo "$$found"; exit 1; fi

# Delivery bundle: dist/rid-node-kit-$(VERSION).tar.gz (install.sh, both images, guide, SHA256SUMS).
.PHONY: bundle
bundle:
	bash deploy/bundle.sh $(VERSION)

# K1 closing test against the portal's development stack (../data-exchange: `pnpm infra:up` and
# the portal API on :4000). Builds the canal image first.
.PHONY: e2e
e2e: image-canal
	node test/e2e/canal-e2e.mjs

check: fmt-check check-canal-deps
	go vet ./...
	go vet -tags canal ./...
	go mod verify
	$(MAKE) test licenses vuln

# Instant APIs against real SQL Server, PostgreSQL, MySQL and Oracle (test/engines), then the
# portal's lint on the OpenAPI each engine generated. RID_IT_ENV is an env file outside the repo
# with RID_IT_ADMIN_PASSWORD, RID_IT_READER_PASSWORD and RID_IT_WRITER_PASSWORD (DESARROLLO.md).
RID_IT_ENV ?= $(HOME)/.config/rid-node/engines.env
ENGINES    := docker compose -f test/engines/compose.yaml --env-file $(RID_IT_ENV)

.PHONY: test-engines engines-up engines-down
engines-up:
	$(ENGINES) up -d --wait --wait-timeout 600

engines-down:
	$(ENGINES) down -v

test-engines: engines-up
	rm -rf internal/instant/testdata/out
	set -a && . $(RID_IT_ENV) && set +a && go test -tags integration -count=1 ./internal/instant/...
	for f in internal/instant/testdata/out/*.json; do node spikes/tools/rid-lint.mts $$f || exit 1; done

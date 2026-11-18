# The node image: one static binary on an empty base, non-root, no shell.
#   docker build -f deploy/Dockerfile --build-arg VARIANT=canal -t den-node:<version>-canal .
# VARIANT=completo (proxy + instant services) | canal (secure channel only, no database drivers).
ARG GO_IMAGE=golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280

# The build stage runs natively and cross-compiles for the target platform (amd64 or arm64).
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VARIANT=completo
ARG VERSION=0.1.0-dev
# No default here: BuildKit fills TARGETOS/TARGETARCH from --platform only when the stage declares
# them bare. With a default (=amd64) every image got an x86-64 binary, the arm64 ones too.
ARG TARGETOS
ARG TARGETARCH
RUN case "$VARIANT" in completo) tags="" ;; canal) tags="canal" ;; *) echo "VARIANT must be completo or canal" >&2; exit 1 ;; esac \
 && CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" go build -trimpath -tags "$tags" \
      -ldflags "-s -w -X github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo.Version=${VERSION}" \
      -o /out/den-node ./cmd/den-node

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chown=65532:65532 deploy/rootfs/ /
COPY --from=build /out/den-node /den-node
COPY LICENSE NOTICE /licenses/
LABEL org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Centre for Digital Public Infrastructure (CDPI)"
USER 65532:65532
ENV DEN_STATE_DIR=/var/lib/den-node DEN_CONFIG=/etc/den-node/node.yaml
ENTRYPOINT ["/den-node", "run"]

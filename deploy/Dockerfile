# The node image: one static binary on an empty base, non-root, no shell.
#   docker build -f deploy/Dockerfile --build-arg VARIANT=canal -t rid-node:<version>-canal .
# VARIANT=completo (proxy + instant services) | canal (secure channel only, no database drivers).
ARG GO_IMAGE=golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280

FROM ${GO_IMAGE} AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VARIANT=completo
ARG VERSION=0.1.0-dev
RUN case "$VARIANT" in completo) tags="" ;; canal) tags="canal" ;; *) echo "VARIANT must be completo or canal" >&2; exit 1 ;; esac \
 && CGO_ENABLED=0 go build -trimpath -tags "$tags" \
      -ldflags "-s -w -X github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo.Version=${VERSION}" \
      -o /out/rid-node ./cmd/rid-node

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chown=65532:65532 deploy/rootfs/ /
COPY --from=build /out/rid-node /rid-node
USER 65532:65532
ENV RID_STATE_DIR=/var/lib/rid-node RID_CONFIG=/etc/rid-node/node.yaml
ENTRYPOINT ["/rid-node", "run"]

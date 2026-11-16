# THROWAWAY SPIKE. The node image: one static binary on an empty base, non-root, no shell.
# Build the binary first: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/gonode-linux-amd64 .
FROM scratch
COPY --chown=65532:65532 rootfs/ /
COPY bin/gonode-linux-amd64 /gonode
COPY service.yaml /service.yaml
USER 65532:65532
ENV RID_SERVICE_FILE=/service.yaml RID_STATE_DIR=/var/lib/rid-node
ENTRYPOINT ["/gonode", "run"]

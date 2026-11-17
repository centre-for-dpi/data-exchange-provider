#!/usr/bin/env bash
# Builds the delivery bundle: dist/den-node-kit-<version>.tar.gz with install.sh, compose.yaml,
# both images (for servers without access to a registry), the guides, licenses, an SBOM per variant
# and SHA256SUMS. With DEN_SIGN_KEY (an OpenSSH private key, kept outside the repo) it also signs
# the bundle: dist/den-node-kit-<version>.tar.gz.sig (ssh-keygen -Y, namespace den-kit).
#   deploy/bundle.sh <version>
set -euo pipefail
VERSION=${1:?usage: deploy/bundle.sh <version>}
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/dist/den-node-kit-$VERSION"
export MSYS_NO_PATHCONV=1
cd "$ROOT"
rm -rf "$OUT" && mkdir -p "$OUT/images"
# docker on Windows (Git Bash) needs a Windows path for --output.
native_path() { if command -v cygpath >/dev/null; then cygpath -w "$1"; else printf '%s' "$1"; fi; }
for arch in amd64 arm64; do
  for variant in completo canal; do
    suffix=$([[ $variant == canal ]] && echo -canal || true)
    docker buildx build -q --platform "linux/$arch" -f deploy/Dockerfile --build-arg VARIANT=$variant       --build-arg VERSION="$VERSION" -t "den-node:$VERSION$suffix"       --output "type=docker,dest=$(native_path "$OUT/images/den-node-$VERSION$suffix-$arch.tar")" . >/dev/null
  done
done
install -m 0755 deploy/install.sh "$OUT/install.sh"
install -m 0644 deploy/compose.yaml deploy/env.example LICENSE NOTICE "$OUT/"
mkdir -p "$OUT/es"
for doc in INSTALL.md INSTANT-APIS.md; do
  install -m 0644 "docs/$doc" "$OUT/"
  install -m 0644 "docs/es/$doc" "$OUT/es/"
done
echo "$VERSION" >"$OUT/VERSION"
{
  echo "# Go modules linked into den-node $VERSION (completo; canal is a subset)"
  go run ./tools/licensecheck -cyclonedx "$(native_path "$OUT/sbom-completo.cdx.json")" -version "$VERSION" ./cmd/den-node
} >"$OUT/LICENSES.txt"
go run ./tools/licensecheck -tags canal -cyclonedx "$(native_path "$OUT/sbom-canal.cdx.json")" -version "$VERSION" ./cmd/den-node >/dev/null
sums=$(cd "$OUT" && find . -type f | sed 's|^\./||' | sort | xargs sha256sum)
printf '%s\n' "$sums" >"$OUT/SHA256SUMS"
tar -C "$ROOT/dist" -czf "$ROOT/dist/den-node-kit-$VERSION.tar.gz" "den-node-kit-$VERSION"
# The bundle's own checksum is published apart from the bundle (portal, operator's channel), so
# an entity can tell a genuine bundle from a tampered one before opening it.
(cd "$ROOT/dist" && sha256sum "den-node-kit-$VERSION.tar.gz" >"den-node-kit-$VERSION.tar.gz.sha256")
rm -f "$ROOT/dist/den-node-kit-$VERSION.tar.gz.sig"
if [[ -n ${DEN_SIGN_KEY:-} ]]; then
  ssh-keygen -q -Y sign -f "$DEN_SIGN_KEY" -n den-kit "$ROOT/dist/den-node-kit-$VERSION.tar.gz"
  echo "dist/den-node-kit-$VERSION.tar.gz.sig  (signature; verified with the operator's public key)"
fi
echo "dist/den-node-kit-$VERSION.tar.gz"
echo "dist/den-node-kit-$VERSION.tar.gz.sha256  (publish it through a channel other than the bundle's)"

#!/usr/bin/env bash
# Builds the delivery bundle: dist/rid-node-kit-<version>.tar.gz with install.sh, compose.yaml,
# both images (for servers without access to a registry), the guide, licenses and SHA256SUMS.
#   deploy/bundle.sh <version>
set -euo pipefail
VERSION=${1:?usage: deploy/bundle.sh <version>}
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/dist/rid-node-kit-$VERSION"
export MSYS_NO_PATHCONV=1
cd "$ROOT"
rm -rf "$OUT" && mkdir -p "$OUT/images"
for variant in completo canal; do
  suffix=$([[ $variant == canal ]] && echo -canal || true)
  docker build -q -f deploy/Dockerfile --build-arg VARIANT=$variant --build-arg VERSION="$VERSION" -t "rid-node:$VERSION$suffix" . >/dev/null
  docker save "rid-node:$VERSION$suffix" >"$OUT/images/rid-node-$VERSION$suffix.tar"
done
install -m 0755 deploy/install.sh "$OUT/install.sh"
install -m 0644 deploy/compose.yaml deploy/env.example "$OUT/"
[[ -f docs/INSTALACION.md ]] && install -m 0644 docs/INSTALACION.md "$OUT/"
echo "$VERSION" >"$OUT/VERSION"
{
  echo "# Go modules linked into rid-node $VERSION (completo; canal is a subset)"
  go run ./tools/licensecheck ./cmd/rid-node
} >"$OUT/LICENSES.txt"
sums=$(cd "$OUT" && find . -type f | sed 's|^\./||' | sort | xargs sha256sum)
printf '%s\n' "$sums" >"$OUT/SHA256SUMS"
tar -C "$ROOT/dist" -czf "$ROOT/dist/rid-node-kit-$VERSION.tar.gz" "rid-node-kit-$VERSION"
echo "dist/rid-node-kit-$VERSION.tar.gz"

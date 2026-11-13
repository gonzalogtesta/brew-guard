#!/bin/sh
# Builds brew-guard for every release target into dist/ and writes SHA256SUMS.
# usage: scripts/build-release.sh v0.1.0
# The build has no cgo, no VCS stamping and no local paths, so building the
# same commit twice gives the same bytes: run it twice and compare SHA256SUMS.
# It needs GNU tar (Linux); the release workflow runs it there.
set -eu

version="${1:?usage: build-release.sh vX.Y.Z}"
case "$version" in
v[0-9]*.[0-9]*.[0-9]*) ;;
*) echo "version must look like v1.2.3" >&2; exit 2 ;;
esac

cd "$(dirname "$0")/.."
rm -rf dist
mkdir dist

export CGO_ENABLED=0 GOTOOLCHAIN=go1.27.3
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
	os="${target%/*}"
	arch="${target#*/}"
	out="dist/brew-guard_${version}_${os}_${arch}"
	GOOS="$os" GOARCH="$arch" go build -trimpath -buildvcs=false \
		-ldflags "-s -w -X main.version=${version}" -o "$out" ./cmd/brew-guard
	tar -C dist -czf "$out.tar.gz" --owner=0 --group=0 --numeric-owner --mtime='1970-01-01 00:00:00' \
		--transform "s|brew-guard_${version}_${os}_${arch}|brew-guard|" "brew-guard_${version}_${os}_${arch}"
	rm "$out"
done

cd dist
sha256sum ./*.tar.gz | sed 's|\./||' > SHA256SUMS
cat SHA256SUMS

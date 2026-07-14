#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="${GO_BUILD_IMAGE:-golang:1.24-bookworm}"

command -v docker >/dev/null 2>&1 || {
	echo "docker is required to build Linux arm64 and amd64 plugins" >&2
	exit 1
}

mkdir -p "$ROOT_DIR/dist"

docker run --rm \
	--platform linux/arm64 \
	-v "$ROOT_DIR:/src" \
	-w /src \
	"$IMAGE" \
	go test ./...

for arch in arm64 amd64; do
	output="dist/xai-autoban-linux-${arch}.so"
	docker run --rm \
		--platform "linux/${arch}" \
		-v "$ROOT_DIR:/src" \
		-w /src \
		-e OUTPUT="$output" \
		"$IMAGE" \
		sh -ceu 'CGO_ENABLED=1 go build -buildvcs=false -buildmode=c-shared -trimpath -ldflags="-s -w" -o "$OUTPUT" .'
	echo "Built $output"
done

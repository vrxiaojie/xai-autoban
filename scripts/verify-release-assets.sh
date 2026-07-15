#!/usr/bin/env bash
set -euo pipefail

assets_dir="${1:-release-assets}"
version="${2:?usage: verify-release-assets.sh <assets-dir> <version>}"

platforms=(
	"windows:amd64:.dll"
	"linux:amd64:.so"
	"linux:arm64:.so"
	"darwin:amd64:.dylib"
	"darwin:arm64:.dylib"
)

for platform in "${platforms[@]}"; do
	IFS=: read -r goos goarch ext <<< "$platform"
	asset="xai-autoban_${version}_${goos}_${goarch}.zip"
	asset_path="${assets_dir}/${asset}"
	library="xai-autoban${ext}"

	test -f "$asset_path" || {
		echo "missing release asset: $asset" >&2
		exit 1
	}

	entries=$(unzip -Z1 "$asset_path")
	if [[ "$entries" != "$library" ]]; then
		echo "$asset must contain only $library at the ZIP root; found:" >&2
		printf '%s\n' "$entries" >&2
		exit 1
	fi
done

test -f "${assets_dir}/checksums.txt" || {
	echo "missing release asset: checksums.txt" >&2
	exit 1
}

checksum_count=$(grep -Ec '^[0-9a-f]{64}  xai-autoban_.+\.zip$' "${assets_dir}/checksums.txt")
if [[ "$checksum_count" -ne "${#platforms[@]}" ]]; then
	echo "checksums.txt must contain ${#platforms[@]} ZIP checksums" >&2
	exit 1
fi

(
	cd "$assets_dir"
	sha256sum -c checksums.txt
)

echo "Verified ${#platforms[@]} CPA plugin-store packages for version $version"

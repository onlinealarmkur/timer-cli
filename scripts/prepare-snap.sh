#!/usr/bin/env bash

set -euo pipefail

die() {
	echo "prepare-snap: $*" >&2
	exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"

version="${1:-}"
arch="${2:-}"
release_dir="${3:-dist}"
project_dir="${4:-}"

validation_error=""
if ! validation_error="$(bash "$script_dir/validate-version.sh" "$version" 2>&1)"; then
	die "$validation_error"
fi
case "$arch" in
	amd64 | arm64) ;;
	*) die "architecture must be one of: amd64, arm64" ;;
esac
[[ -n "$project_dir" && "$project_dir" != "." && "$project_dir" != "/" ]] ||
	die "project directory must not be empty or the repository root"

cd "$repo_root"
release_abs="$(cd "$release_dir" && pwd -P)" || die "release directory not found: $release_dir"
project_parent="$(dirname "$project_dir")"
mkdir -p "$project_parent"
project_parent_abs="$(cd "$project_parent" && pwd -P)"
project_abs="$project_parent_abs/$(basename "$project_dir")"
[[ "$project_abs" != "$repo_root" && "$project_abs" != "/" ]] ||
	die "project directory must not be the repository root"
[[ ! -e "$project_abs" ]] || die "project directory already exists: $project_abs"

archive="$release_abs/timer-cli_${version}_linux_${arch}.tar.gz"
[[ -f "$archive" ]] || die "verified Linux archive not found: $archive"

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/timer-cli-snap.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT INT TERM
tar -xzf "$archive" -C "$temp_root"
archive_root="$temp_root/timer-cli_${version}_linux_${arch}"
binary="$archive_root/timer-cli"
[[ -f "$binary" && -x "$binary" ]] || die "timer-cli binary missing from Linux archive"
for notice in LICENSE THIRD_PARTY_LICENSES; do
	[[ -f "$archive_root/$notice" && ! -L "$archive_root/$notice" ]] ||
		die "$notice missing from Linux archive"
done

mkdir -p "$project_abs/snap" "$project_abs/payload"
install -m 0755 "$binary" "$project_abs/payload/timer-cli"
install -m 0644 "$archive_root/LICENSE" "$archive_root/THIRD_PARTY_LICENSES" "$project_abs/payload/"
sed \
	-e "s/@VERSION@/$version/g" \
	-e "s/@ARCH@/$arch/g" \
	"$repo_root/packaging/snap/snapcraft.yaml.tmpl" >"$project_abs/snap/snapcraft.yaml"

if grep -Eq '@(VERSION|ARCH)@' "$project_abs/snap/snapcraft.yaml"; then
	die "unresolved placeholder in generated snapcraft.yaml"
fi

echo "Snapcraft project prepared at $project_abs"

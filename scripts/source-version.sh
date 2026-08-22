#!/usr/bin/env bash

set -euo pipefail

die() {
	echo "source-version: $*" >&2
	exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
cd "$repo_root"

go_bin="${GO:-go}"
command -v "$go_bin" >/dev/null 2>&1 || die "required tool not found: $go_bin"

source_output="$("$go_bin" run -mod=readonly ./cmd/timer-cli version)" || die "source version command failed"
[[ "$source_output" =~ ^timer-cli\ ([0-9]+\.[0-9]+\.[0-9]+)$ ]] || die "unexpected version output: expected 'timer-cli <version>'"

version="${BASH_REMATCH[1]}"
validation_error=""
if ! validation_error="$(bash "$script_dir/validate-version.sh" "$version" 2>&1)"; then
	die "$validation_error"
fi

printf '%s\n' "$version"

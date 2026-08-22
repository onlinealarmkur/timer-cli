#!/usr/bin/env bash

set -euo pipefail

die() {
	echo "release-docs: $*" >&2
	exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"

version="${1:-}"
readme="${2:-$repo_root/README.md}"

validation_error=""
if ! validation_error="$(bash "$script_dir/validate-version.sh" "$version" 2>&1)"; then
	die "$validation_error"
fi
[[ -f "$readme" && ! -L "$readme" ]] || die "README must be a regular non-symlink file: $readme"

install_pattern='^go install github\.com/onlinealarmkur/timer-cli/cmd/timer-cli@v[0-9]+\.[0-9]+\.[0-9]+$'
install_line="go install github.com/onlinealarmkur/timer-cli/cmd/timer-cli@v$version"
install_count="$(grep -Ec "$install_pattern" -- "$readme" || true)"
[[ "$install_count" == 1 ]] ||
	die "README must contain exactly one stable-version Go install command"
grep -Fqx "$install_line" -- "$readme" ||
	die "README Go install command must use v$version"

archive_pattern='^VERSION=[0-9]+\.[0-9]+\.[0-9]+$'
archive_line="VERSION=$version"
archive_count="$(grep -Ec "$archive_pattern" -- "$readme" || true)"
[[ "$archive_count" == 1 ]] ||
	die "README must contain exactly one stable VERSION assignment"
grep -Fqx "$archive_line" -- "$readme" ||
	die "README archive example must use VERSION=$version"

printf 'README release examples match timer-cli %s\n' "$version"

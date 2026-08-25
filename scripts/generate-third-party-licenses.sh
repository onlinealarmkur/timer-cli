#!/usr/bin/env bash

set -euo pipefail

die() {
	echo "generate-third-party-licenses: $*" >&2
	exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
go_bin="${GO:-go}"

[[ -x "$go_bin" ]] || command -v "$go_bin" >/dev/null 2>&1 || die "Go executable not found: $go_bin"
[[ -f "$repo_root/.go-version" ]] || die ".go-version is missing"
release_go_version="$(tr -d '\n' <"$repo_root/.go-version")"
[[ "$release_go_version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
	die ".go-version must contain one MAJOR.MINOR.PATCH version"

goroot="$("$go_bin" env GOROOT)"
[[ -f "$goroot/LICENSE" ]] || die "Go license not found under GOROOT: $goroot/LICENSE"

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/timer-cli-licenses.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT INT TERM
modules="$temp_root/modules"
template='{{with .Module}}{{if and (not .Main) .Version}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}{{end}}'

for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
	goos="${target%/*}"
	goarch="${target#*/}"
	(
		cd "$repo_root"
		GOWORK=off CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
			"$go_bin" list -mod=readonly -deps -f "$template" ./cmd/timer-cli
	)
done | sed '/^$/d' | LC_ALL=C sort -u >"$modules"

[[ -s "$modules" ]] || die "no linked third-party modules were discovered"

append_text_file() {
	local file="$1"
	local line
	[[ -s "$file" ]] || die "notice file is empty: $file"
	while IFS= read -r line || [[ -n "$line" ]]; do
		printf '%s\n' "$line"
	done <"$file"
}

component_count=0
append_component_notices() {
	local component="$1"
	local source="$2"
	local component_dir="$3"
	local license_file="$component_dir/LICENSE"
	local patents_file="$component_dir/PATENTS"
	[[ -f "$license_file" && ! -L "$license_file" ]] || die "license is not a regular file: $license_file"
	if ((component_count > 0)); then
		printf '\n'
	fi
	component_count=$((component_count + 1))
	printf '%s\n' \
		'-------------------------------------------------------------------------------' \
		"Component: $component" \
		"Source: $source" \
		''
	append_text_file "$license_file"
	if [[ -e "$patents_file" || -L "$patents_file" ]]; then
		[[ -f "$patents_file" && ! -L "$patents_file" ]] || die "patents notice is not a regular file: $patents_file"
		printf '%s\n' '' 'Additional IP rights grant (PATENTS):' ''
		append_text_file "$patents_file"
	fi
}

cat <<'EOF'
timer-cli third-party software notices
======================================

This file contains license notices for software included in timer-cli release
distributions. The timer-cli project license is provided separately in LICENSE.

EOF

append_component_notices "Go runtime and standard library $release_go_version" "https://go.dev/" "$goroot"

while IFS='|' read -r module version module_dir; do
	[[ -n "$module" && -n "$version" && -n "$module_dir" ]] || die "invalid Go module metadata"
	append_component_notices "$module $version" "https://pkg.go.dev/$module@$version" "$module_dir"
done <"$modules"

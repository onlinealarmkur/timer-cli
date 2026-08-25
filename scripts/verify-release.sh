#!/usr/bin/env bash

set -euo pipefail

die() {
	echo "verify-release: $*" >&2
	exit 1
}

require_tool() {
	command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"

version="${1:-}"
export_dir="${2:-}"
validation_error=""
if ! validation_error="$(bash "$script_dir/validate-version.sh" "$version" 2>&1)"; then
	die "$validation_error"
fi
[[ -z "$export_dir" || ( "$export_dir" != "." && "$export_dir" != "/" ) ]] ||
	die "export directory must not be the repository root"

cd "$repo_root"
export LC_ALL=C
export TZ=UTC

tar_bin="${TAR_BIN:-tar}"
date_bin="${DATE_BIN:-date}"
sha256_bin="${SHA256SUM_BIN:-sha256sum}"
go_bin="${GO:-go}"

for tool in bash "$go_bin"; do
	require_tool "$tool"
done

source_version="$(GO="$go_bin" bash "$script_dir/source-version.sh")" || die "source version command failed"
[[ "$source_version" == "$version" ]] ||
	die "source version mismatch: expected 'timer-cli $version', got 'timer-cli $source_version'"

for tool in awk basename cat cmp cp dirname find grep mkdir mktemp rm ruby sed sort tr uname uniq "$tar_bin" "$date_bin" "$sha256_bin"; do
	require_tool "$tool"
done

commit="$(git rev-parse --short=12 HEAD)"
source_date_epoch="$(git log -1 --format=%ct)"
build_date="$("$date_bin" -u -d "@$source_date_epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)" ||
	die "GNU date is required (set DATE_BIN, for example to gdate)"

export_abs=""
if [[ -n "$export_dir" ]]; then
	mkdir -p "$export_dir"
	export_abs="$(cd "$export_dir" && pwd -P)"
	[[ "$export_abs" != "$repo_root" && "$export_abs" != "/" ]] ||
		die "export directory must not be the repository root"
	[[ -z "$(find "$export_abs" -mindepth 1 -maxdepth 1 -print -quit)" ]] ||
		die "export directory must be empty: $export_abs"
fi

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/timer-cli-release-check.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT INT TERM
expected_third_party_licenses="$temp_root/THIRD_PARTY_LICENSES"
GO="$go_bin" bash "$script_dir/generate-third-party-licenses.sh" >"$expected_third_party_licenses"
first="$temp_root/first"
second="$temp_root/second"
mkdir -p "$first" "$second"

bash "$script_dir/package-release.sh" "$version" "$first" "$commit" "$source_date_epoch"
bash "$script_dir/package-release.sh" "$version" "$second" "$commit" "$source_date_epoch"

expected_files="$temp_root/expected-files"
actual_files="$temp_root/actual-files"
printf '%s\n' \
	SHA256SUMS \
	timer-cli.rb \
	"timer-cli_${version}_aur.tar.gz" \
	"timer-cli_${version}_darwin_amd64.tar.gz" \
	"timer-cli_${version}_darwin_arm64.tar.gz" \
	"timer-cli_${version}_linux_amd64.tar.gz" \
	"timer-cli_${version}_linux_arm64.tar.gz" \
	"timer-cli_${version}_source.tar.gz" | sort >"$expected_files"
(
	cd "$first"
	find . -mindepth 1 -maxdepth 1 -type f -print | sed 's#^./##' | sort
) >"$actual_files"
cmp -s "$expected_files" "$actual_files" || die "artifact names differ from the expected release set"
if find "$first" -mindepth 1 -maxdepth 1 ! -type f -print -quit | grep -q .; then
	die "release artifact directory contains a non-regular entry"
fi

while IFS= read -r file; do
	cmp -s "$first/$file" "$second/$file" || die "non-reproducible artifact: $file"
done <"$expected_files"

(
	cd "$first"
	"$sha256_bin" -c SHA256SUMS
) >/dev/null || die "SHA256SUMS verification failed"
(
	cd "$first"
	"$sha256_bin" timer-cli.rb timer-cli_*.tar.gz | sort -k2
) >"$temp_root/actual-sums"
cmp -s "$first/SHA256SUMS" "$temp_root/actual-sums" ||
	die "SHA256SUMS is incomplete, unordered, or incorrect"

targets_root="$temp_root/targets"
mkdir -p "$targets_root"
for goos in darwin linux; do
	for goarch in amd64 arm64; do
		name="timer-cli_${version}_${goos}_${goarch}"
		archive="$first/$name.tar.gz"
		cat >"$temp_root/expected-layout" <<EOF
$name/
$name/LICENSE
$name/README.md
$name/THIRD_PARTY_LICENSES
$name/timer-cli
EOF
		"$tar_bin" -tzf "$archive" >"$temp_root/actual-layout"
		cmp -s "$temp_root/expected-layout" "$temp_root/actual-layout" ||
			die "unexpected archive layout: $(basename "$archive")"

		metadata="$("$tar_bin" --numeric-owner -tvzf "$archive")"
		[[ "$(printf '%s\n' "$metadata" | awk -v path="$name/" '$NF == path {print $1 " " $2}')" == "drwxr-xr-x 0/0" ]] ||
			die "invalid directory mode or owner: $name"
		[[ "$(printf '%s\n' "$metadata" | awk -v path="$name/LICENSE" '$NF == path {print $1 " " $2}')" == "-rw-r--r-- 0/0" ]] ||
			die "invalid LICENSE mode or owner: $name"
		[[ "$(printf '%s\n' "$metadata" | awk -v path="$name/README.md" '$NF == path {print $1 " " $2}')" == "-rw-r--r-- 0/0" ]] ||
			die "invalid README mode or owner: $name"
		[[ "$(printf '%s\n' "$metadata" | awk -v path="$name/THIRD_PARTY_LICENSES" '$NF == path {print $1 " " $2}')" == "-rw-r--r-- 0/0" ]] ||
			die "invalid THIRD_PARTY_LICENSES mode or owner: $name"
		[[ "$(printf '%s\n' "$metadata" | awk -v path="$name/timer-cli" '$NF == path {print $1 " " $2}')" == "-rwxr-xr-x 0/0" ]] ||
			die "invalid timer-cli mode or owner: $name"

		target_dir="$targets_root/$name"
		mkdir -p "$target_dir"
		"$tar_bin" -xzf "$archive" -C "$target_dir"
		target_binary="$target_dir/$name/timer-cli"
		for reviewed_file in LICENSE README.md; do
			cmp -s "$reviewed_file" "$target_dir/$name/$reviewed_file" ||
				die "$reviewed_file differs from the reviewed source: $name"
		done
		cmp -s "$expected_third_party_licenses" "$target_dir/$name/THIRD_PARTY_LICENSES" ||
			die "THIRD_PARTY_LICENSES differs from the generated release notices: $name"
		build_info="$("$go_bin" version -m "$target_binary")" ||
			die "could not read Go build metadata: $name"
		actual_goos="$(printf '%s\n' "$build_info" | awk '$1 == "build" && $2 ~ /^GOOS=/ {sub(/^GOOS=/, "", $2); print $2}')"
		actual_goarch="$(printf '%s\n' "$build_info" | awk '$1 == "build" && $2 ~ /^GOARCH=/ {sub(/^GOARCH=/, "", $2); print $2}')"
		[[ "$actual_goos" == "$goos" ]] ||
			die "binary target OS mismatch for $name: expected $goos, got ${actual_goos:-missing}"
		[[ "$actual_goarch" == "$goarch" ]] ||
			die "binary target architecture mismatch for $name: expected $goarch, got ${actual_goarch:-missing}"
	done
done

host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$host_os" in
	darwin | linux) ;;
	*) host_os="" ;;
esac
host_arch="$(uname -m)"
case "$host_arch" in
	x86_64) host_arch="amd64" ;;
	aarch64 | arm64) host_arch="arm64" ;;
	*) host_arch="" ;;
esac
if [[ -n "$host_os" && -n "$host_arch" ]]; then
	host_name="timer-cli_${version}_${host_os}_${host_arch}"
	actual_version="$("$targets_root/$host_name/$host_name/timer-cli" version)"
	expected_version="timer-cli $version (commit $commit, built $build_date)"
	[[ "$actual_version" == "$expected_version" ]] ||
		die "host binary version metadata mismatch: $actual_version"
else
	echo "verify-release: host binary execution skipped for unsupported $(uname -s)/$(uname -m)" >&2
fi

source_name="timer-cli-$version"
source_archive="$first/timer-cli_${version}_source.tar.gz"
"$tar_bin" -tzf "$source_archive" >"$temp_root/source-layout"
[[ -z "$(sort "$temp_root/source-layout" | uniq -d)" ]] || die "source archive contains duplicate paths"
awk -v root="$source_name/" '
	index($0, root) != 1 || $0 ~ /(^|\/)\.\.($|\/)/ || $0 ~ /^\// { exit 1 }
' "$temp_root/source-layout" || die "source archive contains an unsafe or incorrectly rooted path"
for required in go.mod go.sum vendor/modules.txt cmd/timer-cli/main.go LICENSE README.md THIRD_PARTY_LICENSES; do
	grep -Fxq "$source_name/$required" "$temp_root/source-layout" ||
		die "source archive is missing $required"
done

source_extract="$temp_root/source-extract"
mkdir -p "$source_extract"
"$tar_bin" -xzf "$source_archive" -C "$source_extract"
for reviewed_file in LICENSE README.md; do
	cmp -s "$reviewed_file" "$source_extract/$source_name/$reviewed_file" ||
		die "$reviewed_file differs from the reviewed source archive"
done
cmp -s "$expected_third_party_licenses" "$source_extract/$source_name/THIRD_PARTY_LICENSES" ||
	die "source archive THIRD_PARTY_LICENSES differs from the generated release notices"
source_binary="$temp_root/source-timer-cli"
(
	cd "$source_extract/$source_name"
	GOWORK=off CGO_ENABLED=0 "$go_bin" build -mod=vendor -trimpath -buildvcs=false \
		-ldflags="-s -w -buildid= -X github.com/onlinealarmkur/timer-cli/internal/version.Version=$version" \
		-o "$source_binary" ./cmd/timer-cli
)
[[ "$("$source_binary" version)" == "timer-cli $version" ]] ||
	die "vendored source archive did not build the expected version"

source_sha256="$("$sha256_bin" "$source_archive" | awk '{print $1}')"
[[ "$source_sha256" =~ ^[0-9a-f]{64}$ ]] || die "could not calculate source archive SHA-256"

formula="$first/timer-cli.rb"
ruby -c "$formula" >/dev/null || die "generated Homebrew formula is not valid Ruby syntax"
[[ "$(sed -n '1p' "$formula")" == 'class TimerCli < Formula' ]] ||
	die "generated Homebrew formula has an unexpected header"
expected_formula_url="https://github.com/onlinealarmkur/timer-cli/releases/download/v${version}/timer-cli_${version}_source.tar.gz"
expected_pkgbuild_url="https://github.com/onlinealarmkur/timer-cli/releases/download/v\${pkgver}/timer-cli_\${pkgver}_source.tar.gz"
formula_urls="$(sed -n 's/^[[:space:]]*url[[:space:]]*"\([^"]*\)"[[:space:]]*$/\1/p' "$formula")"
[[ "$formula_urls" == "$expected_formula_url" ]] ||
	die "generated Homebrew formula URL mismatch"
formula_sha256="$(sed -n 's/^[[:space:]]*sha256[[:space:]][[:space:]]*"\([^"]*\)"[[:space:]]*$/\1/p' "$formula")"
[[ "$formula_sha256" == "$source_sha256" ]] ||
	die "generated Homebrew formula checksum does not match the source archive"
grep -Fq '"-mod=vendor"' "$formula" || die "generated Homebrew formula must build from vendored dependencies"
! grep -Eq '@(VERSION|SOURCE_SHA256)@' "$formula" || die "generated Homebrew formula contains an unresolved placeholder"

aur_name="timer-cli_${version}_aur"
aur_archive="$first/$aur_name.tar.gz"
printf '%s\n' "$aur_name/" "$aur_name/.SRCINFO" "$aur_name/LICENSE" "$aur_name/PKGBUILD" >"$temp_root/expected-aur-layout"
"$tar_bin" -tzf "$aur_archive" >"$temp_root/actual-aur-layout"
cmp -s "$temp_root/expected-aur-layout" "$temp_root/actual-aur-layout" ||
	die "unexpected AUR bundle layout"
aur_extract="$temp_root/aur-extract"
mkdir -p "$aur_extract"
"$tar_bin" -xzf "$aur_archive" -C "$aur_extract"
pkgbuild="$aur_extract/$aur_name/PKGBUILD"
srcinfo="$aur_extract/$aur_name/.SRCINFO"
bash -n "$pkgbuild" || die "generated AUR PKGBUILD is not valid Bash syntax"
grep -Fq 'Zero-Clause BSD' "$aur_extract/$aur_name/LICENSE" || die "AUR bundle is missing its packaging license"

grep -Fq "$expected_pkgbuild_url" "$pkgbuild" || die "generated AUR PKGBUILD has the wrong source URL"
grep -Fq "$expected_formula_url" "$srcinfo" || die "generated AUR .SRCINFO has the wrong source URL"
for package_file in "$pkgbuild" "$srcinfo"; do
	grep -Fq "$source_sha256" "$package_file" || die "generated AUR metadata has the wrong source checksum"
	! grep -Eq '@(VERSION|SOURCE_SHA256)@' "$package_file" || die "generated AUR metadata contains an unresolved placeholder"
done
grep -Fxq "pkgver=$version" "$pkgbuild" || die "generated AUR PKGBUILD has the wrong version"
grep -Fq 'go build -mod=vendor' "$pkgbuild" || die "generated AUR PKGBUILD must build from vendored dependencies"

for snap_arch in amd64 arm64; do
	snap_project="$temp_root/snap-$snap_arch"
	bash "$script_dir/prepare-snap.sh" "$version" "$snap_arch" "$first" "$snap_project" >/dev/null
	grep -Fxq "version: '$version'" "$snap_project/snap/snapcraft.yaml" ||
		die "generated Snapcraft project has the wrong version"
	grep -Fq "  $snap_arch:" "$snap_project/snap/snapcraft.yaml" ||
		die "generated Snapcraft project has the wrong architecture"
	! grep -Eq '@(VERSION|ARCH)@' "$snap_project/snap/snapcraft.yaml" ||
		die "generated Snapcraft project contains an unresolved placeholder"
	cmp -s "$snap_project/payload/timer-cli" \
		"$targets_root/timer-cli_${version}_linux_${snap_arch}/timer-cli_${version}_linux_${snap_arch}/timer-cli" ||
		die "Snapcraft payload does not match the verified Linux binary for $snap_arch"
	cmp -s LICENSE "$snap_project/payload/LICENSE" ||
		die "Snapcraft LICENSE does not match the reviewed source for $snap_arch"
	cmp -s "$expected_third_party_licenses" "$snap_project/payload/THIRD_PARTY_LICENSES" ||
		die "Snapcraft THIRD_PARTY_LICENSES differs from the generated release notices for $snap_arch"
done

if [[ -n "$export_abs" ]]; then
	while IFS= read -r file; do
		cp "$first/$file" "$export_abs/$file"
	done <"$expected_files"
	echo "release artifacts verified for timer-cli $version and written to $export_abs"
else
	echo "release artifacts verified for timer-cli $version"
fi

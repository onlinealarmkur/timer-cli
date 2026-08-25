#!/usr/bin/env bash

set -euo pipefail

die() {
	echo "package-release: $*" >&2
	exit 1
}

require_tool() {
	command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"

version="${1:-}"
output_dir="${2:-dist}"
commit="${3:-${COMMIT:-}}"
source_date_epoch="${4:-${SOURCE_DATE_EPOCH:-}}"

validation_error=""
if ! validation_error="$(bash "$script_dir/validate-version.sh" "$version" 2>&1)"; then
	die "$validation_error"
fi
[[ -n "$output_dir" && "$output_dir" != "." && "$output_dir" != "/" ]] || die "output directory must not be the repository root"

cd "$repo_root"
export LC_ALL=C
export TZ=UTC

go_bin="${GO:-go}"
tar_bin="${TAR_BIN:-tar}"
date_bin="${DATE_BIN:-date}"
sha256_bin="${SHA256SUM_BIN:-sha256sum}"

require_tool "$go_bin"

go_version_file="$repo_root/.go-version"
[[ -f "$go_version_file" ]] || die ".go-version must exist at the repository root"
release_go_version=""
exec 3<"$go_version_file"
if ! IFS= read -r release_go_version <&3; then
	exec 3<&-
	die ".go-version must contain exactly one MAJOR.MINOR.PATCH version followed by a newline"
fi
extra_go_version_content=""
if IFS= read -r extra_go_version_content <&3 || [[ -n "$extra_go_version_content" ]]; then
	exec 3<&-
	die ".go-version must contain exactly one MAJOR.MINOR.PATCH version followed by a newline"
fi
exec 3<&-
[[ "$release_go_version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
	die ".go-version must contain exactly one MAJOR.MINOR.PATCH version followed by a newline"

actual_go_version="$("$go_bin" env GOVERSION)"
[[ "$actual_go_version" == "go$release_go_version" ]] ||
	die "Go toolchain mismatch: .go-version requires go$release_go_version, found '$actual_go_version'"

for tool in git "$tar_bin" "$date_bin" gzip "$sha256_bin" chmod cp dirname find grep install mkdir mktemp rm sed sort; do
	require_tool "$tool"
done

"$tar_bin" --version 2>/dev/null | grep -q "GNU tar" || die "GNU tar is required (set TAR_BIN, for example to gtar)"
"$date_bin" --version >/dev/null 2>&1 || die "GNU date is required (set DATE_BIN, for example to gdate)"

git_root="$(git rev-parse --show-toplevel 2>/dev/null)" || die "repository root must be a Git worktree"
git_root="$(cd "$git_root" && pwd -P)"
[[ "$git_root" == "$repo_root" ]] || die "repository root must be a Git worktree"

worktree_status="$(git status --porcelain=v1 --untracked-files=normal --ignored=no)" ||
	die "could not inspect repository state"
[[ -z "$worktree_status" ]] ||
	die "working tree must be clean: commit or remove tracked and unignored untracked changes before packaging"

source_version="$(GO="$go_bin" bash "$script_dir/source-version.sh")" || die "source version command failed"
[[ "$source_version" == "$version" ]] ||
	die "source version mismatch: requested 'timer-cli $version', source is 'timer-cli $source_version'"
bash "$script_dir/validate-release-docs.sh" "$version" || die "release documentation validation failed"

if [[ -z "$commit" ]]; then
	commit="$(git rev-parse --short=12 HEAD)"
fi
[[ "$commit" =~ ^[0-9a-fA-F]{7,40}$ ]] || die "commit must be a 7-40 character hexadecimal Git revision"

if [[ -z "$source_date_epoch" ]]; then
	source_date_epoch="$(git log -1 --format=%ct)"
fi
if [[ ! "$source_date_epoch" =~ ^[0-9]+$ ]] || ((source_date_epoch <= 0)); then
	die "source epoch must be a positive Unix timestamp"
fi
build_date="$("$date_bin" -u -d "@$source_date_epoch" +%Y-%m-%dT%H:%M:%SZ)" || die "source epoch could not be formatted"

[[ -f LICENSE && -f README.md ]] ||
	die "LICENSE and README.md must exist at the repository root"
[[ ! -e THIRD_PARTY_LICENSES && ! -L THIRD_PARTY_LICENSES ]] ||
	die "THIRD_PARTY_LICENSES must be generated for release artifacts, not stored at the repository root"
mkdir -p "$output_dir"
output_abs="$(cd "$output_dir" && pwd -P)"
[[ "$output_abs" != "$repo_root" && "$output_abs" != "/" ]] || die "output directory must not be the repository root"
[[ -z "$(find "$output_abs" -mindepth 1 -maxdepth 1 -print -quit)" ]] || die "output directory must be empty: $output_abs"

homebrew_template="$repo_root/packaging/homebrew/timer-cli.rb.tmpl"
aur_pkgbuild_template="$repo_root/packaging/aur/PKGBUILD.tmpl"
aur_srcinfo_template="$repo_root/packaging/aur/SRCINFO.tmpl"
aur_license="$repo_root/packaging/aur/LICENSE"
for template in "$homebrew_template" "$aur_pkgbuild_template" "$aur_srcinfo_template"; do
	[[ -f "$template" ]] || die "packaging template not found: $template"
done
[[ -f "$aur_license" && ! -L "$aur_license" ]] || die "AUR packaging license not found: $aur_license"
bash "$script_dir/validate-packaging-templates.sh" >/dev/null

export SOURCE_DATE_EPOCH="$source_date_epoch"
export COPYFILE_DISABLE=1
umask 022

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/timer-cli-package.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT INT TERM
third_party_licenses="$temp_root/THIRD_PARTY_LICENSES"
GO="$go_bin" bash "$script_dir/generate-third-party-licenses.sh" >"$third_party_licenses"
chmod 0644 "$third_party_licenses"

ldflags="-s -w -buildid= -X github.com/onlinealarmkur/timer-cli/internal/version.Version=$version -X github.com/onlinealarmkur/timer-cli/internal/version.Commit=$commit -X github.com/onlinealarmkur/timer-cli/internal/version.Date=$build_date"

for goos in darwin linux; do
	for goarch in amd64 arm64; do
		name="timer-cli_${version}_${goos}_${goarch}"
		stage="$output_abs/$name"
		mkdir -p "$stage"
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$go_bin" build \
			-mod=readonly -trimpath -buildvcs=false -ldflags="$ldflags" \
			-o "$stage/timer-cli" ./cmd/timer-cli
		cp LICENSE README.md "$stage/"
		cp "$third_party_licenses" "$stage/THIRD_PARTY_LICENSES"
		chmod 0755 "$stage/timer-cli"
		chmod 0644 "$stage/LICENSE" "$stage/README.md" "$stage/THIRD_PARTY_LICENSES"
		"$tar_bin" --sort=name --format=ustar --owner=0 --group=0 --numeric-owner \
			--mtime="@$source_date_epoch" -C "$output_abs" -cf - "$name" \
			| gzip -n -9 >"$output_abs/$name.tar.gz"
		rm -rf "$stage"
	done
done

source_name="timer-cli-$version"
source_parent="$temp_root/source"
source_stage="$source_parent/$source_name"
mkdir -p "$source_stage"
while IFS= read -r -d '' path; do
	[[ -f "$path" && ! -L "$path" ]] || die "source archive input must be a regular non-symlink file: $path"
	mkdir -p "$source_stage/$(dirname "$path")"
	cp -p "$path" "$source_stage/$path"
	if [[ -x "$path" ]]; then
		chmod 0755 "$source_stage/$path"
	else
		chmod 0644 "$source_stage/$path"
	fi
done < <(git ls-files -z --cached)

cp "$third_party_licenses" "$source_stage/THIRD_PARTY_LICENSES"
chmod 0644 "$source_stage/THIRD_PARTY_LICENSES"

rm -rf "$source_stage/vendor"
(
	cd "$source_stage"
	GOWORK=off "$go_bin" mod vendor
)
find "$source_stage/vendor" -type d -exec chmod 0755 {} +
find "$source_stage/vendor" -type f -exec chmod 0644 {} +

source_archive="$output_abs/timer-cli_${version}_source.tar.gz"
"$tar_bin" --sort=name --format=ustar --owner=0 --group=0 --numeric-owner \
	--mtime="@$source_date_epoch" -C "$source_parent" -cf - "$source_name" \
	| gzip -n -9 >"$source_archive"
source_sha256="$("$sha256_bin" "$source_archive" | sed 's/[[:space:]].*$//')"
[[ "$source_sha256" =~ ^[0-9a-f]{64}$ ]] || die "could not calculate source archive SHA-256"

render_template() {
	local input="$1"
	local output="$2"
	sed \
		-e "s/@VERSION@/$version/g" \
		-e "s/@SOURCE_SHA256@/$source_sha256/g" \
		"$input" >"$output"
	if grep -Eq '@(VERSION|SOURCE_SHA256)@' "$output"; then
		die "unresolved placeholder in generated packaging file: $output"
	fi
}

render_template "$homebrew_template" "$output_abs/timer-cli.rb"
chmod 0644 "$output_abs/timer-cli.rb"

aur_name="timer-cli_${version}_aur"
aur_stage="$temp_root/$aur_name"
mkdir -p "$aur_stage"
render_template "$aur_pkgbuild_template" "$aur_stage/PKGBUILD"
render_template "$aur_srcinfo_template" "$aur_stage/.SRCINFO"
cp "$aur_license" "$aur_stage/LICENSE"
chmod 0644 "$aur_stage/PKGBUILD" "$aur_stage/.SRCINFO" "$aur_stage/LICENSE"
"$tar_bin" --sort=name --format=ustar --owner=0 --group=0 --numeric-owner \
	--mtime="@$source_date_epoch" -C "$temp_root" -cf - "$aur_name" \
	| gzip -n -9 >"$output_abs/$aur_name.tar.gz"

(
	cd "$output_abs"
	"$sha256_bin" timer-cli.rb timer-cli_*.tar.gz | sort -k2 >SHA256SUMS
)

echo "release artifacts written to $output_abs"

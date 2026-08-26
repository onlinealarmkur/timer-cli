#!/usr/bin/env bash

# Template variables such as ${pkgver} must remain literal for the generated
# package metadata rather than expand while this validator runs.
# shellcheck disable=SC2016

set -euo pipefail

die() {
	echo "validate-packaging-templates: $*" >&2
	exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"

homebrew="${HOMEBREW_TEMPLATE:-$repo_root/packaging/homebrew/timer-cli.rb.tmpl}"
aur_pkgbuild="${AUR_PKGBUILD_TEMPLATE:-$repo_root/packaging/aur/PKGBUILD.tmpl}"
aur_srcinfo="${AUR_SRCINFO_TEMPLATE:-$repo_root/packaging/aur/SRCINFO.tmpl}"
snapcraft="${SNAPCRAFT_TEMPLATE:-$repo_root/packaging/snap/snapcraft.yaml.tmpl}"
aur_license="${AUR_LICENSE:-$repo_root/packaging/aur/LICENSE}"

for template in "$homebrew" "$aur_pkgbuild" "$aur_srcinfo" "$snapcraft" "$aur_license"; do
	[[ -f "$template" && ! -L "$template" ]] || die "packaging template must be a regular non-symlink file: $template"
done

exact_line() {
	local file="$1"
	local line="$2"
	local description="$3"
	[[ "$(grep -Fxc "$line" "$file")" -eq 1 ]] || die "$description"
}

expected_url='https://github.com/onlinealarmkur/timer-cli/releases/download/v@VERSION@/timer-cli_@VERSION@_source.tar.gz'
expected_aur_pkgbuild_url='https://github.com/onlinealarmkur/timer-cli/releases/download/v${pkgver}/timer-cli_${pkgver}_source.tar.gz'
expected_homepage='https://onlinealarmkur.com/timer/en/'
[[ "$(sed -n '1p' "$homebrew")" == 'class TimerCli < Formula' ]] ||
	die "Homebrew template must begin with the formula class declaration"
exact_line "$homebrew" 'class TimerCli < Formula' "Homebrew template must define TimerCli exactly once"
exact_line "$homebrew" "  url \"$expected_url\"" "Homebrew template must use the exact generated source archive URL"
exact_line "$homebrew" '  sha256 "@SOURCE_SHA256@"' "Homebrew template must use the generated source checksum exactly once"
exact_line "$homebrew" '  license all_of: ["MIT", "BSD-3-Clause"]' \
	"Homebrew template must declare the combined runtime licenses"
exact_line "$homebrew" '  depends_on "go" => :build' "Homebrew template must declare Go as a build dependency"
exact_line "$homebrew" '    system "go", "build", *std_go_args(ldflags: ldflags), "-mod=vendor", "./cmd/timer-cli"' \
	"Homebrew template must build timer-cli from vendored dependencies"
exact_line "$homebrew" '    pkgshare.install "LICENSE", "THIRD_PARTY_LICENSES"' \
	"Homebrew template must install project and third-party license notices"
[[ "$(grep -Eo '@[A-Z0-9_]+@' "$homebrew" | sort -u)" == $'@SOURCE_SHA256@\n@VERSION@' ]] ||
	die "Homebrew template contains an unexpected placeholder"

exact_line "$aur_pkgbuild" 'pkgname=timer-cli' "AUR PKGBUILD template must use the timer-cli package name"
[[ "$(sed -n '1p' "$aur_pkgbuild")" == '# Maintainer: Burak Ozdemir' ]] ||
	die "AUR PKGBUILD template must begin with the non-sensitive maintainer attribution"
exact_line "$aur_pkgbuild" '# Maintainer: Burak Ozdemir' \
	"AUR PKGBUILD template must contain the maintainer attribution exactly once"
exact_line "$aur_pkgbuild" 'pkgver=@VERSION@' "AUR PKGBUILD template must use the generated version"
exact_line "$aur_pkgbuild" "url='$expected_homepage'" "AUR PKGBUILD template must use the product homepage"
exact_line "$aur_pkgbuild" "source=(\"\${pkgname}-\${pkgver}.tar.gz::$expected_aur_pkgbuild_url\")" \
	"AUR PKGBUILD template must use the exact generated source archive URL"
exact_line "$aur_pkgbuild" "sha256sums=('@SOURCE_SHA256@')" "AUR PKGBUILD template must use the generated source checksum"
grep -Fq 'go build -mod=vendor' "$aur_pkgbuild" || die "AUR PKGBUILD template must build from vendored dependencies"
exact_line "$aur_pkgbuild" "license=('MIT AND BSD-3-Clause')" \
	"AUR PKGBUILD template must declare the combined runtime licenses"
exact_line "$aur_pkgbuild" '  install -Dm644 THIRD_PARTY_LICENSES "${pkgdir}/usr/share/licenses/${pkgname}/THIRD_PARTY_LICENSES"' \
	"AUR PKGBUILD template must install third-party license notices"
[[ "$(grep -Eo '@[A-Z0-9_]+@' "$aur_pkgbuild" | sort -u)" == $'@SOURCE_SHA256@\n@VERSION@' ]] ||
	die "AUR PKGBUILD template contains an unexpected placeholder"

exact_line "$aur_srcinfo" 'pkgbase = timer-cli' "AUR .SRCINFO template must use the timer-cli package base"
exact_line "$aur_srcinfo" $'\tpkgver = @VERSION@' "AUR .SRCINFO template must use the generated version"
exact_line "$aur_srcinfo" $'\turl = '"$expected_homepage" "AUR .SRCINFO template must use the product homepage"
exact_line "$aur_srcinfo" $'\tsha256sums = @SOURCE_SHA256@' "AUR .SRCINFO template must use the generated source checksum"
exact_line "$aur_srcinfo" $'\tlicense = MIT AND BSD-3-Clause' \
	"AUR .SRCINFO must declare the combined runtime licenses"
grep -Fq "$expected_url" "$aur_srcinfo" || die "AUR .SRCINFO template must use the exact generated source archive URL"
[[ "$(grep -Eo '@[A-Z0-9_]+@' "$aur_srcinfo" | sort -u)" == $'@SOURCE_SHA256@\n@VERSION@' ]] ||
	die "AUR .SRCINFO template contains an unexpected placeholder"
grep -Fq 'Zero-Clause BSD' "$aur_license" || die "AUR packaging files must include their 0BSD license"

exact_line "$snapcraft" 'name: timer-cli' "Snapcraft template must use the timer-cli package name"
exact_line "$snapcraft" "version: '@VERSION@'" "Snapcraft template must use the generated version"
exact_line "$snapcraft" 'base: bare' "Snapcraft template must use the bare runtime base"
exact_line "$snapcraft" 'build-base: core26' "Snapcraft template must use the current stable core26 build base"
exact_line "$snapcraft" 'confinement: strict' "Snapcraft template must use strict confinement"
exact_line "$snapcraft" 'license: MIT AND BSD-3-Clause' "Snapcraft template must declare the combined runtime licenses"
exact_line "$snapcraft" '    source: payload' "Snapcraft template must package only the prepared payload"
exact_line "$snapcraft" '      LICENSE: share/doc/timer-cli/LICENSE' \
	"Snapcraft template must install the project license"
exact_line "$snapcraft" '      THIRD_PARTY_LICENSES: share/doc/timer-cli/THIRD_PARTY_LICENSES' \
	"Snapcraft template must install third-party license notices"
[[ "$(grep -Eo '@[A-Z0-9_]+@' "$snapcraft" | sort -u)" == $'@ARCH@\n@VERSION@' ]] ||
	die "Snapcraft template contains an unexpected placeholder"
[[ "$(grep -Fo '@ARCH@' "$snapcraft" | wc -l | tr -d ' ')" == 3 ]] ||
	die "Snapcraft template must apply the generated architecture to platform, build-on, and build-for"
if grep -Eq '^[[:space:]]*plugs:' "$snapcraft"; then
	die "Snapcraft template must not request runtime interfaces"
fi

echo "packaging templates validated"

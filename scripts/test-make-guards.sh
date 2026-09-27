#!/usr/bin/env bash

# Fixture scripts are intentionally literal; their environment is set per test.
# shellcheck disable=SC2016
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
make_bin="$(command -v make)"
temp_root="$(mktemp -d "${TMPDIR:-/tmp}/timer-cli-make-guards.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT INT TERM

mkdir -p "$temp_root/scripts" "$temp_root/failing-tools"
cp "$repo_root/Makefile" "$temp_root/Makefile"
cat >"$temp_root/scripts/generate-third-party-licenses.sh" <<'EOF'
set -eu
case "$TIMER_CLI_NOTICE_FIXTURE" in
  failed) exit 7 ;;
  empty) exit 0 ;;
  missing-header) printf 'Component: Go runtime and standard library 1.27.0\n' ;;
  missing-runtime) printf 'timer-cli third-party software notices\n' ;;
  valid|partial-failure)
    printf '%s\n' 'timer-cli third-party software notices' 'Component: Go runtime and standard library 1.27.0'
    if [ "$TIMER_CLI_NOTICE_FIXTURE" = partial-failure ]; then exit 7; fi ;;
esac
EOF
cat >"$temp_root/scripts/source-version.sh" <<'EOF'
set -eu
test "${GO:-}" = "$TIMER_CLI_EXPECTED_GO"
printf '1.2.3\n'
EOF
printf '#!/bin/sh\nexit 7\n' >"$temp_root/failing-tools/mktemp"
chmod +x "$temp_root/failing-tools/mktemp"

for mode in failed empty missing-header missing-runtime partial-failure; do
  if TIMER_CLI_NOTICE_FIXTURE="$mode" "$make_bin" -s -C "$temp_root" license-check >"$temp_root/output" 2>&1; then
    echo "make-guards: license-check accepted $mode notice" >&2
    exit 1
  fi
done
TIMER_CLI_NOTICE_FIXTURE=valid "$make_bin" -s -C "$temp_root" license-check >"$temp_root/output"
if PATH="$temp_root/failing-tools:$PATH" TIMER_CLI_NOTICE_FIXTURE=valid \
  "$make_bin" -s -C "$temp_root" license-check >"$temp_root/output" 2>&1; then
  echo "make-guards: license-check accepted mktemp failure" >&2
  exit 1
fi

# GO is supplied only as a Make command-line override, not inherited from this
# script's environment. Dry runs exercise version discovery without packaging.
for target in package package-check; do
  output="$(env -u GO TIMER_CLI_EXPECTED_GO=/timer-cli-fixture/go \
    "$make_bin" -s -n -C "$temp_root" "$target" GO=/timer-cli-fixture/go)"
  [[ "$output" == *'GO="/timer-cli-fixture/go"'* && "$output" == *'"1.2.3"'* ]] || {
    echo "make-guards: $target ignored the GO override: $output" >&2
    exit 1
  }
  output="$(env -u GO TIMER_CLI_EXPECTED_GO=must-not-run \
    "$make_bin" -s -n -C "$temp_root" "$target" GO=/timer-cli-fixture/go VERSION=1.2.4)"
  [[ "$output" == *'"1.2.4"'* ]] || {
    echo "make-guards: $target ignored the VERSION override: $output" >&2
    exit 1
  }
done

echo "Make license and version guards passed"

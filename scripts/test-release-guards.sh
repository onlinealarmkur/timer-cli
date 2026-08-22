#!/usr/bin/env bash

# The single-quoted strings in this fixture are literal workflow and generated
# shell snippets. Dynamic sources invoke the fixture functions declared below,
# and their exported variables intentionally stay inside subshells.
# shellcheck disable=SC1003,SC1090,SC2016,SC2030,SC2031,SC2329

set -euo pipefail

die() {
	echo "test-release-guards: $*" >&2
	exit 1
}

expect_failure() {
	local name="$1"
	local expected="$2"
	shift 2
	local output status

	# Do not invoke fixtures from an if/! condition. Bash suppresses errexit for
	# an entire function in that context, even when a sourced fixture enables it.
	set +e
	output="$("$@" 2>&1)"
	status=$?
	set -e
	if ((status == 0)); then
		die "$name unexpectedly succeeded"
	fi
	[[ "$output" == *"$expected"* ]] || die "$name returned unexpected error: $output"
	guard_count=$((guard_count + 1))
}

expect_success() {
	local name="$1"
	shift
	local output status

	set +e
	output="$("$@" 2>&1)"
	status=$?
	set -e
	if ((status != 0)); then
		die "$name failed: $output"
	fi
	guard_count=$((guard_count + 1))
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
go_bin="${GO:-go}"
command -v "$go_bin" >/dev/null 2>&1 || die "required tool not found: $go_bin"

source_version="$(GO="$go_bin" bash "$script_dir/source-version.sh")" || die "could not read source version"
IFS=. read -r source_major source_minor source_patch <<<"$source_version"
mismatch_version="$source_major.$source_minor.$((source_patch + 1))"

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/timer-cli-release-guards.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT INT TERM

workflow="$repo_root/.github/workflows/release.yml"
transferred_validator="$temp_root/validate-transferred-artifacts.sh"
[[ "$(grep -Fxc '          # BEGIN TRANSFERRED ARTIFACT VALIDATION' "$workflow")" -eq 1 ]] ||
	die "transferred artifact validator begin marker must appear exactly once"
[[ "$(grep -Fxc '          # END TRANSFERRED ARTIFACT VALIDATION' "$workflow")" -eq 1 ]] ||
	die "transferred artifact validator end marker must appear exactly once"
awk '
	$0 == "          # BEGIN TRANSFERRED ARTIFACT VALIDATION" {
		begin++
		inside = 1
		next
	}
	$0 == "          # END TRANSFERRED ARTIFACT VALIDATION" {
		end++
		inside = 0
		next
	}
	inside {
		if ($0 == "") {
			print ""
			next
		}
		if (substr($0, 1, 10) != "          ") {
			exit 1
		}
		print substr($0, 11)
	}
	END {
		if (begin != 1 || end != 1 || inside) {
			exit 1
		}
	}
' "$workflow" >"$transferred_validator" ||
	die "could not extract transferred artifact validator"

draft_publisher="$temp_root/publish-verified-draft.sh"
[[ "$(grep -Fxc '          # BEGIN GUARDED DRAFT RELEASE PUBLICATION' "$workflow")" -eq 1 ]] ||
	die "guarded draft publisher begin marker must appear exactly once"
[[ "$(grep -Fxc '          # END GUARDED DRAFT RELEASE PUBLICATION' "$workflow")" -eq 1 ]] ||
	die "guarded draft publisher end marker must appear exactly once"
awk '
	$0 == "          # BEGIN GUARDED DRAFT RELEASE PUBLICATION" {
		begin++
		inside = 1
		next
	}
	$0 == "          # END GUARDED DRAFT RELEASE PUBLICATION" {
		end++
		inside = 0
		next
	}
	inside {
		if ($0 == "") {
			print ""
			next
		}
		if (substr($0, 1, 10) != "          ") {
			exit 1
		}
		print substr($0, 11)
	}
	END {
		if (begin != 1 || end != 1 || inside) {
			exit 1
		}
	}
' "$workflow" >"$draft_publisher" || die "could not extract guarded draft publisher"
bash -n "$draft_publisher" || die "guarded draft publisher has invalid shell syntax"

topology_fail() {
	echo "release topology: $*" >&2
	return 1
}

extract_job_section() {
	local candidate="$1"
	local job="$2"
	local header="  $job:"

	[[ "$(grep -Fxc "$header" "$candidate")" -eq 1 ]] || return 1
	TIMER_CLI_HEADER="$header" awk '
		BEGIN {
			header = ENVIRON["TIMER_CLI_HEADER"]
		}
		$0 == header {
			inside = 1
		}
		inside && $0 != header && $0 ~ /^  [A-Za-z0-9_-]+:$/ {
			exit
		}
		inside {
			print
		}
	' "$candidate"
}

extract_job_permissions() {
	awk '
		$0 == "    permissions:" {
			count++
			inside = 1
			next
		}
		inside && $0 ~ /^      / {
			print substr($0, 7)
			next
		}
		inside {
			inside = 0
		}
		END {
			if (count != 1) {
				exit 1
			}
		}
	' "$1"
}

extract_job_needs() {
	awk '
		$0 ~ /^    needs:/ {
			count++
			print substr($0, 5)
			inside = ($0 == "    needs:")
			next
		}
		inside && $0 ~ /^      / {
			print substr($0, 7)
			next
		}
		inside {
			inside = 0
		}
		END {
			if (count != 1) {
				exit 1
			}
		}
	' "$1"
}

job_action_count() {
	local job_file="$1"
	local action="$2"
	TIMER_CLI_ACTION="$action" awk '
		BEGIN {
			action = ENVIRON["TIMER_CLI_ACTION"]
		}
		index($0, "        uses: " action) == 1 ||
		index($0, "      - uses: " action) == 1 {
			count++
		}
		END {
			print count + 0
		}
	' "$job_file"
}

extract_action_artifact_name() {
	local job_file="$1"
	local action="$2"
	TIMER_CLI_ACTION="$action" awk '
		BEGIN {
			action_prefix = "        uses: " ENVIRON["TIMER_CLI_ACTION"]
		}
		index($0, action_prefix) == 1 {
			action_count++
			inside = 1
			next
		}
		inside && $0 ~ /^      - / {
			inside = 0
		}
		inside && index($0, "          name: ") == 1 {
			name_count++
			print substr($0, 17)
		}
		END {
			if (action_count != 1 || name_count != 1) {
				exit 1
			}
		}
	' "$job_file"
}

extract_action_artifact_values() {
	local job_file="$1"
	local action="$2"
	local key="$3"
	TIMER_CLI_ACTION="$action" TIMER_CLI_KEY="$key" awk '
		BEGIN {
			action_prefix = "        uses: " ENVIRON["TIMER_CLI_ACTION"]
			value_prefix = "          " ENVIRON["TIMER_CLI_KEY"] ": "
		}
		index($0, action_prefix) == 1 {
			action_count++
			inside = 1
			next
		}
		inside && $0 ~ /^      - / {
			inside = 0
		}
		inside && index($0, value_prefix) == 1 {
			value_count++
			print substr($0, length(value_prefix) + 1)
		}
		END {
			if (action_count == 0 || value_count != action_count) {
				exit 1
			}
		}
	' "$job_file"
}

extract_validation_run() {
	local job_file="$1"
	awk '
		$0 == "      - name: Validate transferred artifact set" {
			step_count++
			inside = 1
			next
		}
		inside && $0 ~ /^      - / {
			inside = 0
		}
		inside && $0 ~ /^        run:/ {
			run_count++
			print substr($0, 9)
		}
		END {
			if (step_count != 1 || run_count != 1) {
				exit 1
			}
		}
	' "$job_file"
}

text_count() {
	local candidate="$1"
	local text="$2"
	TIMER_CLI_TEXT="$text" awk '
		BEGIN {
			text = ENVIRON["TIMER_CLI_TEXT"]
		}
		{
			line = $0
			while ((position = index(line, text)) != 0) {
				count++
				line = substr(line, position + length(text))
			}
		}
		END {
			print count + 0
		}
	' "$candidate"
}

validate_completion_shell_install() {
	local verify_step="$2"
	TIMER_CLI_VERIFY_STEP="$verify_step" awk '
		BEGIN {
			verify_step = ENVIRON["TIMER_CLI_VERIFY_STEP"]
		}
		$0 == "      - name: Install completion test shells" {
			install_count++
			install_line = NR
			if (getline != 1 || $0 != "        run: |") {
				exit 1
			}
			if (getline != 1 || $0 != "          sudo apt-get update") {
				exit 1
			}
			if (getline != 1 || $0 != "          sudo apt-get install --yes fish shellcheck zsh") {
				exit 1
			}
			next
		}
		$0 == "      - name: " verify_step {
			verify_count++
			verify_line = NR
		}
		END {
			if (install_count != 1 || verify_count != 1 ||
				install_line >= verify_line) {
				exit 1
			}
		}
	' "$1"
}

validate_release_topology() {
	local candidate="$1"
	local check_dir="$temp_root/topology-check"
	local verify_job="$check_dir/verify.yml"
	local snap_job="$check_dir/snap.yml"
	local assemble_job="$check_dir/assemble.yml"
	local attest_job="$check_dir/attest.yml"
	local release_job="$check_dir/release.yml"
	local actual
	local base_artifact_name='timer-cli-base-${{ github.ref_name }}-${{ github.run_id }}'
	local snap_artifact_name='timer-cli-snap-${{ matrix.arch }}-${{ github.ref_name }}-${{ github.run_id }}'
	local release_artifact_name='timer-cli-release-${{ github.ref_name }}-${{ github.run_id }}'
	local attest_run release_run

	rm -rf "$check_dir"
	mkdir -p "$check_dir"
	extract_job_section "$candidate" verify >"$verify_job" ||
		{ topology_fail "verify job must appear exactly once"; return 1; }
	extract_job_section "$candidate" snap >"$snap_job" ||
		{ topology_fail "snap job must appear exactly once"; return 1; }
	extract_job_section "$candidate" assemble >"$assemble_job" ||
		{ topology_fail "assemble job must appear exactly once"; return 1; }
	extract_job_section "$candidate" attest >"$attest_job" ||
		{ topology_fail "attest job must appear exactly once"; return 1; }
	extract_job_section "$candidate" release >"$release_job" ||
		{ topology_fail "release job must appear exactly once"; return 1; }

	validate_completion_shell_install "$verify_job" "Verify source" ||
		{ topology_fail "verify job must install fish, ShellCheck, and zsh before source verification"; return 1; }

	actual="$(extract_job_permissions "$verify_job")" ||
		{ topology_fail "verify permissions must be exactly contents and actions read"; return 1; }
	[[ "$actual" == $'contents: read\nactions: read' ]] ||
		{ topology_fail "verify permissions must be exactly contents and actions read"; return 1; }
	actual="$(extract_job_permissions "$snap_job")" ||
		{ topology_fail "snap permissions must be exactly contents: read"; return 1; }
	[[ "$actual" == "contents: read" ]] ||
		{ topology_fail "snap permissions must be exactly contents: read"; return 1; }
	actual="$(extract_job_permissions "$assemble_job")" ||
		{ topology_fail "assemble permissions must be exactly contents: read"; return 1; }
	[[ "$actual" == "contents: read" ]] ||
		{ topology_fail "assemble permissions must be exactly contents: read"; return 1; }

	actual="$(extract_job_permissions "$attest_job")" ||
		{ topology_fail "attest permissions must grant only contents read and attestation authority"; return 1; }
	[[ "$actual" == $'contents: read\nid-token: write\nattestations: write\nartifact-metadata: write' ]] ||
		{ topology_fail "attest permissions must grant only contents read and attestation authority"; return 1; }

	actual="$(extract_job_permissions "$release_job")" ||
		{ topology_fail "release permissions must be exactly contents: write"; return 1; }
	[[ "$actual" == "contents: write" ]] ||
		{ topology_fail "release permissions must be exactly contents: write"; return 1; }
	[[ "$(grep -Fxc '    environment: release' "$release_job")" -eq 1 ]] ||
		{ topology_fail "release job must use the dedicated release environment"; return 1; }

	actual="$(extract_job_needs "$snap_job")" ||
		{ topology_fail "snap job must depend exactly on verify"; return 1; }
	[[ "$actual" == "needs: verify" ]] ||
		{ topology_fail "snap job must depend exactly on verify"; return 1; }
	actual="$(extract_job_needs "$assemble_job")" ||
		{ topology_fail "assemble job must depend exactly on verify and snap"; return 1; }
	[[ "$actual" == $'needs:\n- verify\n- snap' ]] ||
		{ topology_fail "assemble job must depend exactly on verify and snap"; return 1; }
	actual="$(extract_job_needs "$attest_job")" ||
		{ topology_fail "attest job must depend exactly on assemble"; return 1; }
	[[ "$actual" == "needs: assemble" ]] ||
		{ topology_fail "attest job must depend exactly on assemble"; return 1; }

	actual="$(extract_job_needs "$release_job")" ||
		{ topology_fail "release job must depend exactly on assemble and attest"; return 1; }
	[[ "$actual" == $'needs:\n- assemble\n- attest' ]] ||
		{ topology_fail "release job must depend exactly on assemble and attest"; return 1; }

	if (( $(job_action_count "$assemble_job" "actions/checkout@") != 0 ||
		$(job_action_count "$attest_job" "actions/checkout@") != 0 ||
		$(job_action_count "$release_job" "actions/checkout@") != 0 )); then
		topology_fail "assembly, attest, and release jobs must not check out source"
		return 1
	fi
	if (( $(job_action_count "$verify_job" "actions/upload-artifact@") != 1 ||
		$(job_action_count "$snap_job" "actions/upload-artifact@") != 1 ||
		$(job_action_count "$assemble_job" "actions/upload-artifact@") != 1 ||
		$(job_action_count "$attest_job" "actions/upload-artifact@") != 0 ||
		$(job_action_count "$release_job" "actions/upload-artifact@") != 0 ||
		$(job_action_count "$verify_job" "actions/download-artifact@") != 0 ||
		$(job_action_count "$snap_job" "actions/download-artifact@") != 1 ||
		$(job_action_count "$assemble_job" "actions/download-artifact@") != 3 ||
		$(job_action_count "$attest_job" "actions/download-artifact@") != 1 ||
		$(job_action_count "$release_job" "actions/download-artifact@") != 1 ||
		$(job_action_count "$attest_job" "actions/attest@") != 1 )); then
		topology_fail "upload, download, attest, and publish actions must remain in their designated jobs"
		return 1
	fi
	if [[ "$(grep -Fxc '          overwrite: true' "$verify_job")" -ne 1 ||
		"$(grep -Fxc '          overwrite: true' "$snap_job")" -ne 1 ||
		"$(grep -Fxc '          overwrite: true' "$assemble_job")" -ne 1 ]]; then
		topology_fail "artifact producers must safely replace their stable run artifacts on rerun"
		return 1
	fi
	if (( $(job_action_count "$snap_job" "snapcore/action-build@") != 1 )); then
		topology_fail "snap job must use the pinned official Snapcraft build action exactly once"
		return 1
	fi
	[[ "$(grep -Fxc '        uses: snapcore/action-build@3bdaa03e1ba6bf59a65f84a751d943d549a54e79 # v1' "$snap_job")" -eq 1 ]] ||
		{ topology_fail "snap job must pin the reviewed Snapcraft build action"; return 1; }
	[[ "$(grep -Fxc '          - arch: amd64' "$snap_job")" -eq 1 &&
		"$(grep -Fxc '            runner: ubuntu-24.04' "$snap_job")" -eq 1 &&
		"$(grep -Fxc '          - arch: arm64' "$snap_job")" -eq 1 &&
		"$(grep -Fxc '            runner: ubuntu-24.04-arm' "$snap_job")" -eq 1 ]] ||
		{ topology_fail "snap job must build on native amd64 and arm64 runners"; return 1; }
	[[ "$(grep -Fc 'sudo snap install --dangerous' "$snap_job")" -eq 1 &&
		"$(grep -Fc 'snap run timer-cli version' "$snap_job")" -eq 1 &&
		"$(grep -Fc 'test -f /snap/timer-cli/current/share/doc/timer-cli/THIRD_PARTY_LICENSES' "$snap_job")" -eq 1 ]] ||
		{ topology_fail "snap job must install and exercise each native package"; return 1; }
	if (( $(text_count "$verify_job" '      - name: Require successful main CI for tagged commit') != 1 ||
		$(text_count "$verify_job" '"repos/${GITHUB_REPOSITORY}/actions/workflows/ci.yml/runs?head_sha=${GITHUB_SHA}&event=push&status=completed&per_page=100" \') != 1 ||
		$(text_count "$verify_job" '                and .head_branch == "main"') != 1 ||
		$(text_count "$verify_job" '                and .conclusion == "success"') != 1 )); then
		topology_fail "verify job must require a successful completed main CI run for the tagged commit"
		return 1
	fi
	if (( $(text_count "$candidate" "# BEGIN GUARDED DRAFT RELEASE PUBLICATION") != 1 ||
		$(text_count "$candidate" "# END GUARDED DRAFT RELEASE PUBLICATION") != 1 )); then
		topology_fail "release job must contain exactly one guarded draft publisher"
		return 1
	fi
	if (( $(text_count "$release_job" 'gh release create "$tag"') != 1 ||
		$(text_count "$release_job" '"https://uploads.github.com/repos/${repository}/releases/${target_release_id}/assets?name=${asset_name}" \') != 1 ||
		$(text_count "$release_job" '"repos/${repository}/releases/${target_release_id}" \') != 1 )); then
		topology_fail "guarded publisher must create a draft, then upload and publish by recorded release ID"
		return 1
	fi
	if (( $(text_count "$release_job" '--draft \') != 1 ||
		$(text_count "$release_job" '--verify-tag \') != 1 ||
		$(text_count "$release_job" '--title "$version" \') != 1 ||
		$(text_count "$release_job" '-F draft=false \') != 1 ||
		$(text_count "$release_job" '--request POST \') != 1 ||
		$(text_count "$release_job" '--method PATCH \') != 1 ||
		$(text_count "$release_job" '-H "Authorization: Bearer $GH_TOKEN" \') != 1 )); then
		topology_fail "guarded publisher must create a verified draft with the plain version title before publishing it"
		return 1
	fi
	if (( $(text_count "$release_job" 'verify_live_tag_target') != 4 ||
		$(text_count "$release_job" '"repos/${repository}/git/ref/tags/${tag}" \') != 1 ||
		$(text_count "$release_job" '"repos/${repository}/git/tags/${object_sha}" \') != 1 ||
		$(text_count "$release_job" 'if [[ "$object_sha" != "$GITHUB_SHA" ]]; then') != 1 )); then
		topology_fail "guarded publisher must bind every mutation to the live annotated tag target"
		return 1
	fi
	if (( $(text_count "$release_job" 'gh release upload "$tag"') != 0 ||
		$(text_count "$release_job" 'gh release edit "$tag"') != 0 )); then
		topology_fail "guarded publisher mutations must not address an existing release by tag"
		return 1
	fi
	local allowed_asset
	for allowed_asset in \
		'SHA256SUMS' \
		'timer-cli.rb' \
		'"timer-cli_${version}_aur.tar.gz"' \
		'"timer-cli_${version}_darwin_amd64.tar.gz"' \
		'"timer-cli_${version}_darwin_arm64.tar.gz"' \
		'"timer-cli_${version}_linux_amd64.tar.gz"' \
		'"timer-cli_${version}_linux_arm64.tar.gz"' \
		'"timer-cli_${version}_source.tar.gz"' \
		'"timer-cli_${version}_amd64.snap"' \
		'"timer-cli_${version}_arm64.snap"'; do
		if [[ "$(grep -Fxc "            $allowed_asset" "$release_job")" -ne 1 ]]; then
			topology_fail "guarded publisher asset allowlist must contain exactly ten release files"
			return 1
		fi
	done
	actual="$(awk '
		$0 == "          readonly -a asset_names=(" { inside = 1; next }
		inside && $0 == "          )" { print count + 0; exit }
		inside && $0 ~ /^            / { count++ }
	' "$release_job")"
	[[ "$actual" == 10 ]] ||
		{ topology_fail "guarded publisher asset allowlist must contain exactly ten release files"; return 1; }
	if (( $(text_count "$release_job" 'cmp --silent -- "dist/$asset_name" "$remote_path"') != 1 ||
		$(text_count "$release_job" 'Draft contains an unexpected or duplicate release asset.') != 1 ||
		$(text_count "$release_job" 'Release assets are missing, duplicated, unexpected, or incomplete.') != 1 ||
		$(text_count "$release_job" 'Remote release asset differs from the built asset: $asset_name') != 1 ||
		$(text_count "$release_job" 'Published release state could not be verified.') != 1 ||
		$(text_count "$release_job" 'assert_same_published "$target_release_id"') != 2 ||
		$(text_count "$release_job" 'A matching published release is a read-only successful rerun.') != 1 )); then
		topology_fail "guarded publisher must verify the exact remote asset set, bytes, and published state"
		return 1
	fi
	if (( $(text_count "$release_job" "softprops/action-gh-release@") != 0 )); then
		topology_fail "release publication must not bypass the guarded draft publisher"
		return 1
	fi

	actual="$(extract_action_artifact_name "$verify_job" "actions/upload-artifact@")" ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	[[ "$actual" == "$base_artifact_name" ]] ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	actual="$(extract_action_artifact_name "$snap_job" "actions/download-artifact@")" ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	[[ "$actual" == "$base_artifact_name" ]] ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	actual="$(extract_action_artifact_name "$snap_job" "actions/upload-artifact@")" ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	[[ "$actual" == "$snap_artifact_name" ]] ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	if grep -Eq '^          (pattern|merge-multiple):' "$assemble_job"; then
		topology_fail "assemble job must download exactly the current-run base and snap artifacts"
		return 1
	fi
	actual="$(extract_action_artifact_values "$assemble_job" "actions/download-artifact@" name)" ||
		{ topology_fail "assemble job must download exactly the current-run base and snap artifacts"; return 1; }
	[[ "$actual" == $'timer-cli-base-${{ github.ref_name }}-${{ github.run_id }}\ntimer-cli-snap-amd64-${{ github.ref_name }}-${{ github.run_id }}\ntimer-cli-snap-arm64-${{ github.ref_name }}-${{ github.run_id }}' ]] ||
		{ topology_fail "assemble job must download exactly the current-run base and snap artifacts"; return 1; }
	actual="$(extract_action_artifact_values "$assemble_job" "actions/download-artifact@" path)" ||
		{ topology_fail "assemble job must download every expected input into dist"; return 1; }
	[[ "$actual" == $'dist\ndist\ndist' ]] ||
		{ topology_fail "assemble job must download every expected input into dist"; return 1; }
	actual="$(extract_action_artifact_name "$assemble_job" "actions/upload-artifact@")" ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	[[ "$actual" == "$release_artifact_name" ]] ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	actual="$(extract_action_artifact_name "$attest_job" "actions/download-artifact@")" ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	[[ "$actual" == "$release_artifact_name" ]] ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	actual="$(extract_action_artifact_name "$release_job" "actions/download-artifact@")" ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }
	[[ "$actual" == "$release_artifact_name" ]] ||
		{ topology_fail "artifact transfers must use their exact identities"; return 1; }

	attest_run="$(extract_validation_run "$attest_job")" ||
		{ topology_fail "attest job must execute transferred artifact validation"; return 1; }
	release_run="$(extract_validation_run "$release_job")" ||
		{ topology_fail "release job must execute transferred artifact validation"; return 1; }
	[[ "$attest_run" == "run: &validate-transferred-artifacts |" ]] ||
		{ topology_fail "attest validation must define the transferred-artifact anchor"; return 1; }
	[[ "$release_run" == "run: *validate-transferred-artifacts" ]] ||
		{ topology_fail "release validation must reuse the transferred-artifact alias"; return 1; }
	(( $(text_count "$candidate" "&validate-transferred-artifacts") == 1 )) ||
		{ topology_fail "transferred-artifact validation anchor must appear exactly once"; return 1; }
	(( $(text_count "$candidate" "*validate-transferred-artifacts") == 1 )) ||
		{ topology_fail "transferred-artifact validation alias must appear exactly once"; return 1; }
}

validate_ci_release_toolchain_job() {
	local candidate="$1"
	local check_dir="$temp_root/ci-topology-check"
	local job_file="$check_dir/release-toolchain-quality.yml"

	rm -rf "$check_dir"
	mkdir -p "$check_dir"
	extract_job_section "$candidate" release-toolchain-quality >"$job_file" ||
		{ topology_fail "release-toolchain quality job must appear exactly once"; return 1; }

	[[ "$(grep -Fxc '    runs-on: ubuntu-latest' "$job_file")" -eq 1 ]] ||
		{ topology_fail "release-toolchain quality job must run on Ubuntu"; return 1; }
	[[ "$(grep -Fxc '    timeout-minutes: 30' "$job_file")" -eq 1 ]] ||
		{ topology_fail "release-toolchain quality job must have a finite timeout"; return 1; }
	validate_completion_shell_install "$job_file" "Verify with release toolchain" ||
		{ topology_fail "release-toolchain quality job must install fish, ShellCheck, and zsh before verification"; return 1; }
	[[ "$(job_action_count "$job_file" 'actions/checkout@')" -eq 1 ]] ||
		{ topology_fail "release-toolchain quality job must check out source exactly once"; return 1; }
	[[ "$(job_action_count "$job_file" 'actions/setup-go@')" -eq 1 ]] ||
		{ topology_fail "release-toolchain quality job must set up Go exactly once"; return 1; }
	[[ "$(grep -Fxc '          go-version-file: .go-version' "$job_file")" -eq 1 ]] ||
		{ topology_fail "release-toolchain quality job must use .go-version"; return 1; }
	[[ "$(grep -Fxc '        run: make all' "$job_file")" -eq 1 ]] ||
		{ topology_fail "release-toolchain quality job must run make all exactly once"; return 1; }
}

validate_ci_packaging_topology() {
	local candidate="$1"
	local check_dir="$temp_root/ci-packaging-topology-check"
	local package_job="$check_dir/package.yml"
	local snap_job="$check_dir/package-snap.yml"
	local homebrew_job="$check_dir/package-homebrew.yml"
	local aur_job="$check_dir/package-aur.yml"
	local artifact_name='timer-cli-ci-packages-${{ github.sha }}-${{ github.run_id }}'
	local actual homebrew_matrix

	rm -rf "$check_dir"
	mkdir -p "$check_dir"
	extract_job_section "$candidate" package >"$package_job" ||
		{ topology_fail "CI package job must appear exactly once"; return 1; }
	extract_job_section "$candidate" package-snap >"$snap_job" ||
		{ topology_fail "CI native Snap package job must appear exactly once"; return 1; }
	extract_job_section "$candidate" package-homebrew >"$homebrew_job" ||
		{ topology_fail "CI Homebrew package job must appear exactly once"; return 1; }
	extract_job_section "$candidate" package-aur >"$aur_job" ||
		{ topology_fail "CI AUR package job must appear exactly once"; return 1; }

	[[ "$(grep -Fxc 'permissions:' "$candidate")" -eq 1 &&
		"$(grep -Fxc '  contents: read' "$candidate")" -eq 1 ]] ||
		{ topology_fail "CI packaging jobs must inherit read-only contents permission"; return 1; }
	if grep -Eq 'secrets\.|persist-credentials: true|permissions:[[:space:]]*write' \
		"$package_job" "$snap_job" "$homebrew_job" "$aur_job"; then
		topology_fail "CI packaging jobs must not use secrets or write credentials"
		return 1
	fi

	[[ "$(grep -Fxc '          bash scripts/verify-release.sh "$version" dist' "$package_job")" -eq 1 ]] ||
		{ topology_fail "CI package job must export locally verified release inputs"; return 1; }
	[[ "$(job_action_count "$package_job" 'actions/upload-artifact@')" -eq 1 ]] ||
		{ topology_fail "CI package job must transfer one verified input set"; return 1; }
	actual="$(extract_action_artifact_name "$package_job" 'actions/upload-artifact@')" ||
		{ topology_fail "CI packaging artifact must have an exact identity"; return 1; }
	[[ "$actual" == "$artifact_name" ]] ||
		{ topology_fail "CI packaging artifact must have an exact identity"; return 1; }
	[[ "$(grep -Fxc '          overwrite: true' "$package_job")" -eq 1 ]] ||
		{ topology_fail "CI packaging producer must safely replace its stable run artifact on rerun"; return 1; }
	[[ "$(grep -Fxc "          name: $artifact_name" "$snap_job")" -eq 1 &&
		"$(grep -Fxc "          name: $artifact_name" "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc "          name: $artifact_name" "$aur_job")" -eq 1 ]] ||
		{ topology_fail "every package smoke job must download the current CI run"; return 1; }
	for job_file in "$snap_job" "$homebrew_job" "$aur_job"; do
		[[ "$(job_action_count "$job_file" 'actions/download-artifact@')" -eq 1 ]] ||
			{ topology_fail "every package smoke job must download exactly one verified input set"; return 1; }
		[[ "$(grep -Fxc '          path: dist' "$job_file")" -eq 1 ]] ||
			{ topology_fail "every package smoke job must isolate verified inputs in dist"; return 1; }
	done

	[[ "$(grep -Fxc "    if: github.event_name == 'push'" "$snap_job")" -eq 1 &&
		"$(grep -Fxc "    if: github.event_name == 'push'" "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc "    if: github.event_name == 'push'" "$aur_job")" -eq 1 ]] ||
		{ topology_fail "package-manager smoke jobs must run on main pushes"; return 1; }
	for job_file in "$snap_job" "$homebrew_job" "$aur_job"; do
		actual="$(extract_job_needs "$job_file")" ||
			{ topology_fail "package-manager smoke jobs must depend exactly on package"; return 1; }
		[[ "$actual" == "needs: package" ]] ||
			{ topology_fail "package-manager smoke jobs must depend exactly on package"; return 1; }
	done

	[[ "$(grep -Fxc '        uses: snapcore/action-build@3bdaa03e1ba6bf59a65f84a751d943d549a54e79 # v1' "$snap_job")" -eq 1 ]] ||
		{ topology_fail "CI Snap smoke must use the reviewed pinned build action"; return 1; }
	[[ "$(grep -Fxc '          - arch: amd64' "$snap_job")" -eq 1 &&
		"$(grep -Fxc '            runner: ubuntu-24.04' "$snap_job")" -eq 1 &&
		"$(grep -Fxc '          - arch: arm64' "$snap_job")" -eq 1 &&
		"$(grep -Fxc '            runner: ubuntu-24.04-arm' "$snap_job")" -eq 1 ]] ||
		{ topology_fail "CI Snap smoke must build on native amd64 and arm64 runners"; return 1; }
	[[ "$(grep -Fc 'sudo snap install --dangerous' "$snap_job")" -eq 1 &&
		"$(grep -Fc 'snap run timer-cli version' "$snap_job")" -eq 1 &&
		"$(grep -Fc 'snap run timer-cli 1 segundo --lang es --final-only --no-bell' "$snap_job")" -eq 1 &&
		"$(grep -Fc 'test -f /snap/timer-cli/current/share/doc/timer-cli/THIRD_PARTY_LICENSES' "$snap_job")" -eq 1 ]] ||
		{ topology_fail "CI Snap smoke must install and exercise English and Spanish behavior"; return 1; }

	[[ "$(grep -Fxc '    name: Package smoke (Homebrew ${{ matrix.goos }}/${{ matrix.goarch }})' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '    runs-on: ${{ matrix.runner }}' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '    timeout-minutes: 30' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '      fail-fast: false' "$homebrew_job")" -eq 1 ]] ||
		{ topology_fail "Homebrew package smoke must use the reviewed native target matrix"; return 1; }
	homebrew_matrix="$(awk '
		$0 == "        include:" {
			include_count++
			inside = 1
			next
		}
		inside && $0 == "    steps:" {
			inside = 0
		}
		inside && index($0, "          - runner: ") == 1 {
			if (runner != "") exit 1
			runner = substr($0, 21)
			entry_count++
			next
		}
		inside && index($0, "            goos: ") == 1 {
			goos = substr($0, 19)
			next
		}
		inside && index($0, "            goarch: ") == 1 {
			goarch = substr($0, 21)
			if (runner == "" || goos == "" || goarch == "") exit 1
			print runner "|" goos "|" goarch
			runner = ""
			goos = ""
			goarch = ""
		}
		END {
			if (include_count != 1 || entry_count != 3 || runner != "") exit 1
		}
	' "$homebrew_job")" ||
		{ topology_fail "Homebrew package smoke must use the reviewed native target matrix"; return 1; }
	[[ "$homebrew_matrix" == $'macos-latest|darwin|arm64\nmacos-15-intel|darwin|amd64\nubuntu-24.04|linux|amd64' ]] ||
		{ topology_fail "Homebrew package smoke must cover exact native darwin/arm64, darwin/amd64, and linux/amd64 targets"; return 1; }
	[[ "$(grep -Fxc '          archive_name="timer-cli_${version}_${{ matrix.goos }}_${{ matrix.goarch }}"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          archive="$GITHUB_WORKSPACE/dist/${archive_name}.tar.gz"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          test -f "$archive"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          tar -xzf "$archive" -C "$RUNNER_TEMP"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          binary="$RUNNER_TEMP/$archive_name/timer-cli"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          test -x "$binary"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          test "$("$binary" version)" = "timer-cli $version (commit $commit, built $build_date)"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          test "$("$binary" 1 segundo --lang es --final-only --no-bell)" = "¡Se acabó el tiempo!"' "$homebrew_job")" -eq 1 ]] ||
		{ topology_fail "Homebrew smoke must execute the exact verified target archive before formula installation"; return 1; }
	[[ "$(grep -Fxc '          HOMEBREW_DEVELOPER: "1"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          HOMEBREW_NO_AUTO_UPDATE: "1"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          HOMEBREW_NO_INSTALL_FROM_API: "1"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '            export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          command -v brew' "$homebrew_job")" -eq 1 &&
		"$(grep -Fc '"file://$source_archive"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fc 'brew install --build-from-source "$fixture"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fc 'brew test "$fixture"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          style_formula="$RUNNER_TEMP/homebrew-style/Formula/timer-cli.rb"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          mkdir -p "$(dirname "$style_formula")"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          cp dist/timer-cli.rb "$style_formula"' "$homebrew_job")" -eq 1 &&
		"$(grep -Fxc '          brew style --formula "$style_formula"' "$homebrew_job")" -eq 1 ]] ||
		{ topology_fail "Homebrew smoke must build, test, and style the generated formula with a local source"; return 1; }

	[[ "$(grep -Fxc '      image: archlinux:base-devel-20260809.0.570793@sha256:49facfaf7eac45ed51ea3056091b8478191df5bcd62225e457e89c246b7cbda3' "$aur_job")" -eq 1 ]] ||
		{ topology_fail "AUR package smoke must pin the reviewed official Arch image digest"; return 1; }
	[[ "$(grep -Fc 'makepkg --verifysource --noconfirm' "$aur_job")" -eq 1 &&
		"$(grep -Fc 'makepkg --cleanbuild --clean --noconfirm' "$aur_job")" -eq 1 &&
		"$(grep -Fc 'makepkg --printsrcinfo >.SRCINFO.actual' "$aur_job")" -eq 1 &&
		"$(grep -Fc 'cmp --silent .SRCINFO .SRCINFO.actual' "$aur_job")" -eq 1 &&
		"$(grep -Fc 'pacman -U --noconfirm' "$aur_job")" -eq 1 &&
		"$(grep -Fc 'timer-cli 1 segundo --lang es --final-only --no-bell' "$aur_job")" -eq 1 &&
		"$(grep -Fc 'test -f /usr/share/licenses/timer-cli/THIRD_PARTY_LICENSES' "$aur_job")" -eq 1 ]] ||
		{ topology_fail "AUR smoke must verify, build, compare metadata, install, and exercise the package"; return 1; }
	[[ "$(grep -Fc 'cp "dist/timer-cli_${version}_source.tar.gz" "$package_dir/timer-cli-${version}.tar.gz"' "$aur_job")" -eq 1 ]] ||
		{ topology_fail "AUR smoke must supply the verified source archive locally"; return 1; }
	if grep -Eq '(^|[[:space:]])(curl|wget|git[[:space:]]+clone)([[:space:]]|$)' "$aur_job"; then
		topology_fail "AUR smoke must not fetch package source from the network"
		return 1
	fi
}

topology_fixture="$temp_root/release-topology.yml"
ci_workflow="$repo_root/.github/workflows/ci.yml"
ci_topology_fixture="$temp_root/ci-topology.yml"

mutate_job_line() {
	local job="$1"
	local from="$2"
	local to="$3"
	# awk implementations disagree about escape processing in -v values. Pass
	# complete YAML lines through the environment so a trailing backslash stays
	# literal on both macOS awk and the mawk used by Ubuntu runners.
	TIMER_CLI_JOB="$job" TIMER_CLI_FROM="$from" TIMER_CLI_TO="$to" awk '
		BEGIN {
			header = "  " ENVIRON["TIMER_CLI_JOB"] ":"
			from = ENVIRON["TIMER_CLI_FROM"]
			to = ENVIRON["TIMER_CLI_TO"]
		}
		$0 == header {
			inside = 1
		}
		inside && $0 != header && $0 ~ /^  [A-Za-z0-9_-]+:$/ {
			inside = 0
		}
		inside && !changed && $0 == from {
			print to
			changed = 1
			next
		}
		{
			print
		}
		END {
			if (!changed) {
				exit 1
			}
		}
	' "$workflow" >"$topology_fixture" || die "could not create $job topology mutation"
}

insert_after_job_line() {
	local job="$1"
	local after="$2"
	local addition="$3"
	TIMER_CLI_JOB="$job" TIMER_CLI_AFTER="$after" TIMER_CLI_ADDITION="$addition" awk '
		BEGIN {
			header = "  " ENVIRON["TIMER_CLI_JOB"] ":"
			after = ENVIRON["TIMER_CLI_AFTER"]
			addition = ENVIRON["TIMER_CLI_ADDITION"]
		}
		$0 == header {
			inside = 1
		}
		inside && $0 != header && $0 ~ /^  [A-Za-z0-9_-]+:$/ {
			inside = 0
		}
		{
			print
		}
		inside && !changed && $0 == after {
			print addition
			changed = 1
		}
		END {
			if (!changed) {
				exit 1
			}
		}
	' "$workflow" >"$topology_fixture" || die "could not create $job topology insertion"
}

delete_job_line() {
	local job="$1"
	local line="$2"
	TIMER_CLI_JOB="$job" TIMER_CLI_LINE="$line" awk '
		BEGIN {
			header = "  " ENVIRON["TIMER_CLI_JOB"] ":"
			line = ENVIRON["TIMER_CLI_LINE"]
		}
		$0 == header {
			inside = 1
		}
		inside && $0 != header && $0 ~ /^  [A-Za-z0-9_-]+:$/ {
			inside = 0
		}
		inside && !changed && $0 == line {
			changed = 1
			next
		}
		{
			print
		}
		END {
			if (!changed) {
				exit 1
			}
		}
	' "$workflow" >"$topology_fixture" || die "could not create $job topology deletion"
}

mutate_job_match() {
	local job="$1"
	local pattern="$2"
	local replacement="$3"
	TIMER_CLI_JOB="$job" TIMER_CLI_PATTERN="$pattern" TIMER_CLI_REPLACEMENT="$replacement" awk '
		BEGIN {
			header = "  " ENVIRON["TIMER_CLI_JOB"] ":"
			pattern = ENVIRON["TIMER_CLI_PATTERN"]
			replacement = ENVIRON["TIMER_CLI_REPLACEMENT"]
		}
		$0 == header {
			inside = 1
		}
		inside && $0 != header && $0 ~ /^  [A-Za-z0-9_-]+:$/ {
			inside = 0
		}
		inside && !changed && $0 ~ pattern {
			print replacement
			changed = 1
			next
		}
		{
			print
		}
		END {
			if (!changed) {
				exit 1
			}
		}
	' "$workflow" >"$topology_fixture" || die "could not create $job topology action mutation"
}

mutate_ci_job_line() {
	local job="$1"
	local from="$2"
	local to="$3"
	TIMER_CLI_JOB="$job" TIMER_CLI_FROM="$from" TIMER_CLI_TO="$to" awk '
		BEGIN {
			header = "  " ENVIRON["TIMER_CLI_JOB"] ":"
			from = ENVIRON["TIMER_CLI_FROM"]
			to = ENVIRON["TIMER_CLI_TO"]
		}
		$0 == header { inside = 1 }
		inside && $0 != header && $0 ~ /^  [A-Za-z0-9_-]+:$/ { inside = 0 }
		inside && !changed && $0 == from {
			print to
			changed = 1
			next
		}
		{ print }
		END { if (!changed) exit 1 }
	' "$ci_workflow" >"$ci_topology_fixture" || die "could not create $job CI topology mutation"
}

mutate_ci_job_match() {
	local job="$1"
	local pattern="$2"
	local replacement="$3"
	TIMER_CLI_JOB="$job" TIMER_CLI_PATTERN="$pattern" TIMER_CLI_REPLACEMENT="$replacement" awk '
		BEGIN {
			header = "  " ENVIRON["TIMER_CLI_JOB"] ":"
			pattern = ENVIRON["TIMER_CLI_PATTERN"]
			replacement = ENVIRON["TIMER_CLI_REPLACEMENT"]
		}
		$0 == header { inside = 1 }
		inside && $0 != header && $0 ~ /^  [A-Za-z0-9_-]+:$/ { inside = 0 }
		inside && !changed && $0 ~ pattern {
			print replacement
			changed = 1
			next
		}
		{ print }
		END { if (!changed) exit 1 }
	' "$ci_workflow" >"$ci_topology_fixture" || die "could not create $job CI topology action mutation"
}

# These deliberately unavailable tool names prove every case exits before the
# deterministic packaging prerequisites are inspected.
export TAR_BIN="timer-cli-test-missing-tar"
export DATE_BIN="timer-cli-test-missing-date"
export SHA256SUM_BIN="timer-cli-test-missing-sha256sum"

guard_count=0

readme_fixture="$temp_root/README.md"
cp "$repo_root/README.md" "$readme_fixture"
expect_success "current README release versions" \
	bash "$script_dir/validate-release-docs.sh" "$source_version" "$readme_fixture"
sed "s#@v$source_version#@v$mismatch_version#" "$repo_root/README.md" >"$readme_fixture"
expect_failure "stale README Go install version" "README Go install command must use v$source_version" \
	bash "$script_dir/validate-release-docs.sh" "$source_version" "$readme_fixture"
sed "s/^VERSION=$source_version$/VERSION=$mismatch_version/" "$repo_root/README.md" >"$readme_fixture"
expect_failure "stale README archive version" "README archive example must use VERSION=$source_version" \
	bash "$script_dir/validate-release-docs.sh" "$source_version" "$readme_fixture"

expect_success "release privilege topology" \
	validate_release_topology "$workflow"

expect_success "CI release-toolchain quality topology" \
	validate_ci_release_toolchain_job "$ci_workflow"

expect_success "CI package-manager smoke topology" \
	validate_ci_packaging_topology "$ci_workflow"

mutate_ci_job_line package '          bash scripts/verify-release.sh "$version" dist' \
	'          make package-check VERSION="$version"'
expect_failure "CI verified package export removed" "CI package job must export locally verified release inputs" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package '          overwrite: true' '          overwrite: false'
expect_failure "CI stable artifact overwrite disabled" \
	"CI packaging producer must safely replace its stable run artifact on rerun" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_match package-snap 'uses: snapcore/action-build@' \
	'        uses: snapcore/action-build@main'
expect_failure "CI Snap action pin drift" "CI Snap smoke must use the reviewed pinned build action" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package-homebrew '          - runner: macos-latest' \
	'          - runner: ubuntu-24.04'
expect_failure "CI Homebrew native target drift" \
	"Homebrew package smoke must cover exact native darwin/arm64, darwin/amd64, and linux/amd64 targets" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package-homebrew \
	'          archive_name="timer-cli_${version}_${{ matrix.goos }}_${{ matrix.goarch }}"' \
	'          archive_name="timer-cli_${version}_darwin_arm64"'
expect_failure "CI Homebrew exact archive selection removed" \
	"Homebrew smoke must execute the exact verified target archive before formula installation" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package-homebrew \
	'          test "$("$binary" 1 segundo --lang es --final-only --no-bell)" = "¡Se acabó el tiempo!"' \
	'          true # exact archive execution removed'
expect_failure "CI Homebrew archive behavior smoke removed" \
	"Homebrew smoke must execute the exact verified target archive before formula installation" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package-homebrew '          brew test "$fixture"' '          true # brew test removed'
expect_failure "CI Homebrew test removed" "Homebrew smoke must build, test, and style the generated formula with a local source" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package-homebrew '          brew style --formula "$style_formula"' \
	'          brew style dist/timer-cli.rb'
expect_failure "CI Homebrew formula classification removed" \
	"Homebrew smoke must build, test, and style the generated formula with a local source" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package-aur '            makepkg --verifysource --noconfirm' \
	'            true # source verification removed'
expect_failure "CI AUR source verification removed" "AUR smoke must verify, build, compare metadata, install, and exercise the package" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_ci_job_line package-aur \
	'      image: archlinux:base-devel-20260809.0.570793@sha256:49facfaf7eac45ed51ea3056091b8478191df5bcd62225e457e89c246b7cbda3' \
	'      image: archlinux:base-devel'
expect_failure "CI Arch image pin removed" "AUR package smoke must pin the reviewed official Arch image digest" \
	validate_ci_packaging_topology "$ci_topology_fixture"

mutate_job_line verify "          sudo apt-get install --yes fish shellcheck zsh" "          sudo apt-get install --yes fish zsh"
expect_failure "release verification tool installation drift" "verify job must install fish, ShellCheck, and zsh before source verification" \
	validate_release_topology "$topology_fixture"

mutate_job_line verify "      actions: read" "      actions: write"
expect_failure "verify write permission" "verify permissions must be exactly contents and actions read" \
	validate_release_topology "$topology_fixture"

mutate_job_line verify \
	'            "repos/${GITHUB_REPOSITORY}/actions/workflows/ci.yml/runs?head_sha=${GITHUB_SHA}&event=push&status=completed&per_page=100" \' \
	'            "repos/${GITHUB_REPOSITORY}/actions/workflows/ci.yml/runs?event=push&status=completed&per_page=100" \'
expect_failure "tagged SHA CI binding removed" "verify job must require a successful completed main CI run for the tagged commit" \
	validate_release_topology "$topology_fixture"

mutate_job_line snap "            runner: ubuntu-24.04-arm" "            runner: ubuntu-24.04"
expect_failure "snap native runner drift" "snap job must build on native amd64 and arm64 runners" \
	validate_release_topology "$topology_fixture"

mutate_job_match snap "uses: snapcore/action-build@" "        uses: snapcore/action-build@main"
expect_failure "snap action pin drift" "snap job must pin the reviewed Snapcraft build action" \
	validate_release_topology "$topology_fixture"

mutate_job_line attest "      contents: read" "      contents: write"
expect_failure "attest contents write permission" "attest permissions must grant only contents read and attestation authority" \
	validate_release_topology "$topology_fixture"

insert_after_job_line release "      contents: write" "      id-token: write"
expect_failure "release OIDC permission" "release permissions must be exactly contents: write" \
	validate_release_topology "$topology_fixture"

mutate_job_line attest "    needs: assemble" "    needs: release"
expect_failure "attest dependency drift" "attest job must depend exactly on assemble" \
	validate_release_topology "$topology_fixture"

delete_job_line release "      - assemble"
expect_failure "release dependency drift" "release job must depend exactly on assemble and attest" \
	validate_release_topology "$topology_fixture"

insert_after_job_line attest "    steps:" "      - uses: actions/checkout@fixture"
expect_failure "privileged source checkout" "assembly, attest, and release jobs must not check out source" \
	validate_release_topology "$topology_fixture"

mutate_job_match attest "uses: actions/attest@" "        uses: softprops/action-gh-release@fixture"
expect_failure "release action placement drift" "upload, download, attest, and publish actions must remain in their designated jobs" \
	validate_release_topology "$topology_fixture"

mutate_job_line release "    environment: release" "    environment: staging"
expect_failure "release environment drift" "release job must use the dedicated release environment" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '            gh api --method PATCH \' '            gh api --method GET \'
expect_failure "draft publication removed" "guarded publisher must create a verified draft with the plain version title before publishing it" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '              "repos/${repository}/releases/${target_release_id}" \' \
	'              "repos/${repository}/releases/tags/${tag}" \'
expect_failure "publication addressed through mutable tag" "guarded publisher must create a draft, then upload and publish by recorded release ID" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '                    "https://uploads.github.com/repos/${repository}/releases/${target_release_id}/assets?name=${asset_name}" \' \
	'                    "https://uploads.github.com/repos/${repository}/releases/tags/${tag}/assets?name=${asset_name}" \'
expect_failure "asset upload addressed through mutable tag" "guarded publisher must create a draft, then upload and publish by recorded release ID" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '            verify_live_tag_target' '            true # live tag verification removed'
expect_failure "live tag mutation binding removed" "guarded publisher must bind every mutation to the live annotated tag target" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '            if ! cmp --silent -- "dist/$asset_name" "$remote_path"; then' \
	'            if ! test -s "dist/$asset_name"; then'
expect_failure "remote byte comparison removed" "guarded publisher must verify the exact remote asset set, bytes, and published state" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '              --draft \' '              --latest \'
expect_failure "draft creation removed" "guarded publisher must create a verified draft with the plain version title before publishing it" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '              --title "$version" \' '              --title "$tag" \'
expect_failure "release title prefix drift" "guarded publisher must create a verified draft with the plain version title before publishing it" \
	validate_release_topology "$topology_fixture"

mutate_job_line verify '          overwrite: true' '          overwrite: false'
expect_failure "stable release artifact overwrite disabled" \
	"artifact producers must safely replace their stable run artifacts on rerun" \
	validate_release_topology "$topology_fixture"

mutate_job_line verify '          name: timer-cli-base-${{ github.ref_name }}-${{ github.run_id }}' \
	'          name: timer-cli-base-${{ github.ref_name }}'
expect_failure "base artifact run identity removed" "artifact transfers must use their exact identities" \
	validate_release_topology "$topology_fixture"

mutate_job_line assemble '          name: timer-cli-base-${{ github.ref_name }}-${{ github.run_id }}' \
	'          pattern: timer-cli-*-${{ github.ref_name }}-*'
expect_failure "cross-run artifact wildcard introduced" "assemble job must download exactly the current-run base and snap artifacts" \
	validate_release_topology "$topology_fixture"

delete_job_line assemble '          name: timer-cli-snap-amd64-${{ github.ref_name }}-${{ github.run_id }}'
expect_failure "amd64 snap assembly input omitted" "assemble job must download exactly the current-run base and snap artifacts" \
	validate_release_topology "$topology_fixture"

delete_job_line assemble '          name: timer-cli-snap-arm64-${{ github.ref_name }}-${{ github.run_id }}'
expect_failure "arm64 snap assembly input omitted" "assemble job must download exactly the current-run base and snap artifacts" \
	validate_release_topology "$topology_fixture"

mutate_job_line release '          name: timer-cli-release-${{ github.ref_name }}-${{ github.run_id }}' "          name: wrong-release-artifact"
expect_failure "release artifact identity drift" "artifact transfers must use their exact identities" \
	validate_release_topology "$topology_fixture"

delete_job_line release "        run: *validate-transferred-artifacts"
expect_failure "release validation removed" "release job must execute transferred artifact validation" \
	validate_release_topology "$topology_fixture"

mutate_job_line release "        run: *validate-transferred-artifacts" "        run: *different-validator"
expect_failure "release validator alias drift" "release validation must reuse the transferred-artifact alias" \
	validate_release_topology "$topology_fixture"

publisher_fixture="$temp_root/publisher-fixture"
publisher_commit_sha="1111111111111111111111111111111111111111"
publisher_changed_commit_sha="2222222222222222222222222222222222222222"
publisher_tag_object_sha="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
publisher_changed_tag_object_sha="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
publisher_asset_names=(
	SHA256SUMS
	timer-cli.rb
	"timer-cli_${source_version}_aur.tar.gz"
	"timer-cli_${source_version}_darwin_amd64.tar.gz"
	"timer-cli_${source_version}_darwin_arm64.tar.gz"
	"timer-cli_${source_version}_linux_amd64.tar.gz"
	"timer-cli_${source_version}_linux_arm64.tar.gz"
	"timer-cli_${source_version}_source.tar.gz"
	"timer-cli_${source_version}_amd64.snap"
	"timer-cli_${source_version}_arm64.snap"
)

write_publisher_fixture() {
	local state="$1"
	local asset_name

	rm -rf "$publisher_fixture"
	mkdir -p "$publisher_fixture/dist" "$publisher_fixture/remote"
	printf '%s\n' "$state" >"$publisher_fixture/release-state"
	printf '101\n' >"$publisher_fixture/release-id"
	printf '201\n' >"$publisher_fixture/next-asset-id"
	printf '0\n' >"$publisher_fixture/query-count"
	printf '0\n' >"$publisher_fixture/tag-query-count"
	printf '0\n' >"$publisher_fixture/verified-tag-query-count"
	printf '0\n' >"$publisher_fixture/last-mutation-tag-query-count"
	printf '%s\n' "$publisher_commit_sha" >"$publisher_fixture/tag-target"
	printf '%s\n' "$publisher_tag_object_sha" >"$publisher_fixture/tag-object-sha"
	: >"$publisher_fixture/assets.tsv"
	: >"$publisher_fixture/calls.log"
	: >"$publisher_fixture/mutations.log"
	for asset_name in "${publisher_asset_names[@]}"; do
		printf 'verified fixture bytes for %s\n' "$asset_name" \
			>"$publisher_fixture/dist/$asset_name"
	done
}

fake_publisher_query_ref() {
	local query_count change_on_query

	query_count="$(cat "$publisher_fixture/tag-query-count")"
	query_count=$((query_count + 1))
	printf '%s\n' "$query_count" >"$publisher_fixture/tag-query-count"
	printf 'query-tag-ref\n' >>"$publisher_fixture/calls.log"
	if [[ -f "$publisher_fixture/change-tag-target-on-query" ]]; then
		change_on_query="$(cat "$publisher_fixture/change-tag-target-on-query")"
		if [[ "$query_count" == "$change_on_query" ]]; then
			printf '%s\n' "$publisher_changed_commit_sha" >"$publisher_fixture/tag-target"
			printf '%s\n' "$publisher_changed_tag_object_sha" >"$publisher_fixture/tag-object-sha"
		fi
	fi
	if [[ -f "$publisher_fixture/invalid-tag-ref-response" ]]; then
		printf '[]\n'
		return
	fi
	printf '{"ref":"refs/tags/v%s","object":{"type":"tag","sha":"%s"}}\n' \
		"$source_version" "$(cat "$publisher_fixture/tag-object-sha")"
}

fake_publisher_query_tag() {
	local endpoint="$1"
	local requested_sha="${endpoint##*/}"
	local current_sha

	printf 'query-tag-object\n' >>"$publisher_fixture/calls.log"
	current_sha="$(cat "$publisher_fixture/tag-object-sha")"
	if [[ "$requested_sha" != "$current_sha" ]]; then
		echo "fake gh: annotated tag object not found: $requested_sha" >&2
		return 1
	fi
	printf '{"sha":"%s","object":{"type":"commit","sha":"%s"}}\n' \
		"$current_sha" "$(cat "$publisher_fixture/tag-target")"
	cat "$publisher_fixture/tag-query-count" \
		>"$publisher_fixture/verified-tag-query-count"
}

require_fresh_tag_verification() {
	local verified_count last_mutation_count

	verified_count="$(cat "$publisher_fixture/verified-tag-query-count")"
	last_mutation_count="$(cat "$publisher_fixture/last-mutation-tag-query-count")"
	if (( verified_count <= last_mutation_count )); then
		echo "fake gh: mutation lacked a fresh live tag verification" >&2
		return 1
	fi
	printf '%s\n' "$verified_count" \
		>"$publisher_fixture/last-mutation-tag-query-count"
}

add_publisher_remote_asset() {
	local asset_name="$1"
	local asset_state="$2"
	local asset_id

	asset_id="$(cat "$publisher_fixture/next-asset-id")"
	printf '%s\n' "$((asset_id + 1))" >"$publisher_fixture/next-asset-id"
	printf '%s\t%s\t%s\n' "$asset_id" "$asset_name" "$asset_state" \
		>>"$publisher_fixture/assets.tsv"
	if [[ -f "$publisher_fixture/dist/$asset_name" ]]; then
		cp "$publisher_fixture/dist/$asset_name" "$publisher_fixture/remote/$asset_id"
	else
		printf 'unexpected fixture bytes for %s\n' "$asset_name" \
			>"$publisher_fixture/remote/$asset_id"
	fi
}

add_all_publisher_remote_assets() {
	local asset_name
	for asset_name in "${publisher_asset_names[@]}"; do
		add_publisher_remote_asset "$asset_name" uploaded
	done
}

fake_publisher_query_release() {
	local query_count state release_id is_draft change_on_query

	query_count="$(cat "$publisher_fixture/query-count")"
	query_count=$((query_count + 1))
	printf '%s\n' "$query_count" >"$publisher_fixture/query-count"
	printf 'query-release\n' >>"$publisher_fixture/calls.log"
	state="$(cat "$publisher_fixture/release-state")"
	if [[ "$state" == absent ]]; then
		printf '{"data":{"repository":{"release":null}}}\n'
		return
	fi
	release_id="$(cat "$publisher_fixture/release-id")"
	if [[ -f "$publisher_fixture/change-identity-on-query" ]]; then
		change_on_query="$(cat "$publisher_fixture/change-identity-on-query")"
		if [[ "$query_count" == "$change_on_query" ]]; then
			release_id=$((release_id + 1))
		fi
	fi
	if [[ -f "$publisher_fixture/change-state-on-query" ]]; then
		change_on_query="$(cat "$publisher_fixture/change-state-on-query")"
		if [[ "$query_count" == "$change_on_query" ]]; then
			state=draft
		fi
	fi
	case "$state" in
		draft) is_draft=true ;;
		published) is_draft=false ;;
		*)
			echo "fake gh: invalid release state: $state" >&2
			return 1
			;;
	esac
	printf '{"data":{"repository":{"release":{"databaseId":%s,"isDraft":%s}}}}\n' \
		"$release_id" "$is_draft"
}

fake_publisher_query_assets() {
	printf 'query-assets\n' >>"$publisher_fixture/calls.log"
	jq -Rn '
		[[inputs
		  | split("\t")
		  | {id: (.[0] | tonumber), name: .[1], state: .[2]}]]
	' "$publisher_fixture/assets.tsv"
}

fake_publisher_download_asset() {
	local endpoint="$1"
	local asset_id="${endpoint##*/}"
	local asset_name

	asset_name="$(awk -F '\t' -v id="$asset_id" '$1 == id {print $2}' \
		"$publisher_fixture/assets.tsv")"
	if [[ -z "$asset_name" || ! -f "$publisher_fixture/remote/$asset_id" ]]; then
		echo "fake gh: asset not found: $asset_id" >&2
		return 1
	fi
	printf 'download:%s\n' "$asset_name" >>"$publisher_fixture/calls.log"
	cat "$publisher_fixture/remote/$asset_id"
}

fake_publisher_create_release() {
	require_fresh_tag_verification || return 1
	if [[ "$(cat "$publisher_fixture/release-state")" != absent ]]; then
		echo "fake gh: release already exists" >&2
		return 1
	fi
	printf 'draft\n' >"$publisher_fixture/release-state"
	printf 'create\n' >>"$publisher_fixture/mutations.log"
}

fake_publisher_upload_asset() {
	local endpoint="$1"
	local asset_path="$2"
	local asset_name="${asset_path##*/}"
	local endpoint_without_query="${endpoint%%\?*}"
	local endpoint_release_id="${endpoint_without_query%/assets}"
	local asset_id

	endpoint_release_id="${endpoint_release_id##*/}"
	if [[ "$endpoint_release_id" != "$(cat "$publisher_fixture/release-id")" ]]; then
		echo "fake gh: upload addressed the wrong release ID" >&2
		return 1
	fi
	require_fresh_tag_verification || return 1

	if [[ "$(cat "$publisher_fixture/release-state")" != draft ]]; then
		echo "fake gh: upload requires a draft" >&2
		return 1
	fi
	if [[ ! -f "$publisher_fixture/$asset_path" ]]; then
		echo "fake gh: upload source is missing: $asset_path" >&2
		return 1
	fi
	add_publisher_remote_asset "$asset_name" uploaded
	asset_id="$(awk -F '\t' -v name="$asset_name" '$2 == name {id = $1} END {print id}' \
		"$publisher_fixture/assets.tsv")"
	printf 'upload:%s\n' "$asset_name" >>"$publisher_fixture/mutations.log"
	printf '{"id":%s,"name":"%s","state":"uploaded"}\n' "$asset_id" "$asset_name"
}

fake_publisher_curl() {
	local endpoint="${!#}"
	local asset_path asset_name_query argument previous_argument

	previous_argument=""
	for argument in "$@"; do
		if [[ "$previous_argument" == --data-binary ]]; then
			asset_path="${argument#@}"
		fi
		previous_argument="$argument"
	done
	asset_name_query="${endpoint##*?name=}"
	if [[ " $* " != *' --request POST '* ||
		"$endpoint" != https://uploads.github.com/repos/*/releases/*/assets?name=* ||
		-z "${asset_path:-}" || "${asset_path##*/}" != "$asset_name_query" ]]; then
		echo "fake curl: invalid release asset upload" >&2
		return 1
	fi
	fake_publisher_upload_asset "$endpoint" "$asset_path"
}

fake_publisher_publish_release() {
	local endpoint="$1"
	local endpoint_release_id="${endpoint##*/}"

	if [[ "$endpoint_release_id" != "$(cat "$publisher_fixture/release-id")" ]]; then
		echo "fake gh: publication addressed the wrong release ID" >&2
		return 1
	fi
	require_fresh_tag_verification || return 1
	if [[ "$(cat "$publisher_fixture/release-state")" != draft ]]; then
		echo "fake gh: publication requires a draft" >&2
		return 1
	fi
	printf 'published\n' >"$publisher_fixture/release-state"
	printf 'publish\n' >>"$publisher_fixture/mutations.log"
	if [[ -f "$publisher_fixture/fail-after-publish" ]]; then
		rm -f "$publisher_fixture/fail-after-publish"
		echo "simulated failure immediately after publication" >&2
		exit 1
	fi
	printf '{"id":%s,"draft":false}\n' "$endpoint_release_id"
}

fake_publisher_gh() {
	local endpoint

	if [[ "${1:-}" == api && "${2:-}" == graphql ]]; then
		fake_publisher_query_release
		return
	fi
	endpoint="${!#}"
	if [[ "${1:-}" == api && "$endpoint" == *'/git/ref/tags/'* ]]; then
		fake_publisher_query_ref
		return
	fi
	if [[ "${1:-}" == api && "$endpoint" == *'/git/tags/'* ]]; then
		fake_publisher_query_tag "$endpoint"
		return
	fi
	if [[ "${1:-}" == api && "$endpoint" == *'/assets?per_page=100' ]]; then
		fake_publisher_query_assets
		return
	fi
	if [[ "${1:-}" == api && "$endpoint" == *'/releases/assets/'* ]]; then
		fake_publisher_download_asset "$endpoint"
		return
	fi
	if [[ "${1:-}" == release && "${2:-}" == create ]]; then
		fake_publisher_create_release
		return
	fi
	if [[ "${1:-}" == api && " $* " == *' --method PATCH '* && " $* " == *' draft=false '* ]]; then
		fake_publisher_publish_release "$endpoint"
		return
	fi
	echo "fake gh: unsupported command: $*" >&2
	return 1
}

run_publisher_fixture() {
	(
		cd "$publisher_fixture"
		# The dynamically sourced publisher step calls this command shim.
		# shellcheck disable=SC2317
		gh() {
			fake_publisher_gh "$@"
		}
		# The dynamically sourced publisher step calls this command shim.
		# shellcheck disable=SC2317
		curl() {
			fake_publisher_curl "$@"
		}
		export GITHUB_REF_NAME="v$source_version"
		export GITHUB_SHA="$publisher_commit_sha"
		export GH_TOKEN="publisher-fixture-token"
		export GH_REPO="onlinealarmkur/timer-cli"
		source "$draft_publisher"
	)
}

publisher_state_is() {
	local expected="$1"
	[[ "$(cat "$publisher_fixture/release-state")" == "$expected" ]]
}

publisher_mutations_equal() {
	local expected="$1"
	local actual
	actual="$(cat "$publisher_fixture/mutations.log")"
	if [[ "$actual" != "$expected" ]]; then
		printf 'expected mutations:\n%s\nactual mutations:\n%s\n' "$expected" "$actual" >&2
		return 1
	fi
}

publisher_download_count_is() {
	local expected="$1"
	local actual
	actual="$(awk -F ':' '$1 == "download" {count++} END {print count + 0}' \
		"$publisher_fixture/calls.log")"
	[[ "$actual" == "$expected" ]]
}

expected_new_release_mutations="$(
	printf 'create\n'
	for asset_name in "${publisher_asset_names[@]}"; do
		printf 'upload:%s\n' "$asset_name"
	done
	printf 'publish\n'
)"
expected_resumed_draft_mutations="$(
	for asset_name in "${publisher_asset_names[@]:3}"; do
		printf 'upload:%s\n' "$asset_name"
	done
	printf 'publish\n'
)"

write_publisher_fixture absent
expect_success "absent release is created, verified, and published" \
	run_publisher_fixture
expect_success "new release reaches published state" publisher_state_is published
expect_success "new release performs only expected mutations" publisher_mutations_equal \
	"$expected_new_release_mutations"

write_publisher_fixture draft
add_publisher_remote_asset "${publisher_asset_names[0]}" uploaded
add_publisher_remote_asset "${publisher_asset_names[1]}" uploaded
add_publisher_remote_asset "${publisher_asset_names[2]}" uploaded
expect_success "matching draft resumes and publishes" run_publisher_fixture
expect_success "resumed draft reaches published state" publisher_state_is published
expect_success "resumed draft uploads only missing assets" publisher_mutations_equal \
	"$expected_resumed_draft_mutations"

write_publisher_fixture draft
printf '2\n' >"$publisher_fixture/change-identity-on-query"
expect_failure "draft release ID changes before upload" "Release identity changed while publishing or verifying." \
	run_publisher_fixture
expect_success "draft identity-change failure is read-only" publisher_mutations_equal ""

write_publisher_fixture absent
printf '2\n' >"$publisher_fixture/change-tag-target-on-query"
expect_failure "live tag changes after draft creation" "Live release tag no longer targets the workflow commit." \
	run_publisher_fixture
expect_success "tag-change failure stops before asset upload" publisher_mutations_equal "create"

write_publisher_fixture absent
: >"$publisher_fixture/invalid-tag-ref-response"
expect_failure "invalid live tag API response" "GitHub returned an invalid annotated release tag reference." \
	run_publisher_fixture
expect_success "invalid-tag-response failure is read-only" publisher_mutations_equal ""

write_publisher_fixture published
add_all_publisher_remote_assets
expect_success "matching published release is a successful rerun" run_publisher_fixture
expect_success "published rerun makes no mutations" publisher_mutations_equal ""
expect_success "published rerun compares every asset" publisher_download_count_is 10

write_publisher_fixture published
for asset_name in "${publisher_asset_names[@]:0:9}"; do
	add_publisher_remote_asset "$asset_name" uploaded
done
expect_failure "published release missing an asset" "Release assets are missing, duplicated, unexpected, or incomplete." \
	run_publisher_fixture
expect_success "missing-asset failure is read-only" publisher_mutations_equal ""

write_publisher_fixture published
add_all_publisher_remote_assets
add_publisher_remote_asset unexpected.txt uploaded
expect_failure "published release with an extra asset" "Release assets are missing, duplicated, unexpected, or incomplete." \
	run_publisher_fixture
expect_success "extra-asset failure is read-only" publisher_mutations_equal ""

write_publisher_fixture published
add_all_publisher_remote_assets
add_publisher_remote_asset "${publisher_asset_names[0]}" uploaded
expect_failure "published release with a duplicate asset" "Release assets are missing, duplicated, unexpected, or incomplete." \
	run_publisher_fixture
expect_success "duplicate-asset failure is read-only" publisher_mutations_equal ""

write_publisher_fixture published
for asset_name in "${publisher_asset_names[@]}"; do
	if [[ "$asset_name" == "${publisher_asset_names[0]}" ]]; then
		add_publisher_remote_asset "$asset_name" new
	else
		add_publisher_remote_asset "$asset_name" uploaded
	fi
done
expect_failure "published release with a non-uploaded asset" "Release assets are missing, duplicated, unexpected, or incomplete." \
	run_publisher_fixture
expect_success "non-uploaded-asset failure is read-only" publisher_mutations_equal ""

write_publisher_fixture published
add_all_publisher_remote_assets
mismatched_asset_id="$(awk -F '\t' -v name="${publisher_asset_names[0]}" '$2 == name {print $1}' \
	"$publisher_fixture/assets.tsv")"
printf 'mismatched remote bytes\n' >"$publisher_fixture/remote/$mismatched_asset_id"
expect_failure "published release with mismatched asset bytes" "Remote release asset differs from the built asset: ${publisher_asset_names[0]}" \
	run_publisher_fixture
expect_success "mismatched-asset failure is read-only" publisher_mutations_equal ""

write_publisher_fixture published
add_all_publisher_remote_assets
printf '2\n' >"$publisher_fixture/change-identity-on-query"
expect_failure "published release identity changes during verification" "Release identity changed while publishing or verifying." \
	run_publisher_fixture
expect_success "identity-change failure is read-only" publisher_mutations_equal ""

write_publisher_fixture published
add_all_publisher_remote_assets
printf '2\n' >"$publisher_fixture/change-state-on-query"
expect_failure "published release becomes a draft during verification" "Published release state could not be verified." \
	run_publisher_fixture
expect_success "state-change failure is read-only" publisher_mutations_equal ""

write_publisher_fixture absent
: >"$publisher_fixture/fail-after-publish"
expect_failure "runner fails immediately after publication" "simulated failure immediately after publication" \
	run_publisher_fixture
expect_success "failed publication run still published the release" publisher_state_is published
: >"$publisher_fixture/mutations.log"
: >"$publisher_fixture/calls.log"
expect_success "matching rerun recovers after publication-side failure" run_publisher_fixture
expect_success "recovery rerun makes no mutations" publisher_mutations_equal ""
expect_success "recovery rerun compares every asset" publisher_download_count_is 10

package_fixture="$temp_root/package-fixture"
mkdir -p "$package_fixture/scripts" "$package_fixture/packaging/homebrew" "$package_fixture/packaging/aur" "$package_fixture/packaging/snap"
cp "$script_dir/package-release.sh" "$script_dir/validate-version.sh" \
	"$script_dir/validate-packaging-templates.sh" "$script_dir/validate-release-docs.sh" \
	"$package_fixture/scripts/"
cp "$repo_root/packaging/homebrew/timer-cli.rb.tmpl" "$package_fixture/packaging/homebrew/"
cp "$repo_root/packaging/aur/PKGBUILD.tmpl" "$repo_root/packaging/aur/SRCINFO.tmpl" "$package_fixture/packaging/aur/"
cp "$repo_root/packaging/aur/LICENSE" "$package_fixture/packaging/aur/"
cp "$repo_root/packaging/snap/snapcraft.yaml.tmpl" "$package_fixture/packaging/snap/"

installed_go_version="$("$go_bin" env GOVERSION)"
[[ "$installed_go_version" =~ ^go(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
	die "installed Go version has unexpected format: $installed_go_version"
installed_go_patch="${BASH_REMATCH[3]}"
mismatched_go_version="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((installed_go_patch + 1))"

run_package_fixture() {
	bash "$package_fixture/scripts/package-release.sh" "$source_version"
}

rm -f "$package_fixture/.go-version"
expect_failure "missing release Go version file" ".go-version must exist at the repository root" \
	run_package_fixture

printf '1.26.x\n' >"$package_fixture/.go-version"
expect_failure "malformed release Go version file" ".go-version must contain exactly one MAJOR.MINOR.PATCH version followed by a newline" \
	run_package_fixture

printf '%s' "${installed_go_version#go}" >"$package_fixture/.go-version"
expect_failure "release Go version file without trailing newline" ".go-version must contain exactly one MAJOR.MINOR.PATCH version followed by a newline" \
	run_package_fixture

printf '%s\n%s\n' "${installed_go_version#go}" "$mismatched_go_version" >"$package_fixture/.go-version"
expect_failure "release Go version file with extra line" ".go-version must contain exactly one MAJOR.MINOR.PATCH version followed by a newline" \
	run_package_fixture

printf '%s\n' "$mismatched_go_version" >"$package_fixture/.go-version"
expect_failure "release Go toolchain mismatch" "Go toolchain mismatch: .go-version requires go$mismatched_go_version, found '$installed_go_version'" \
	run_package_fixture

printf '%s\n' "${installed_go_version#go}" >"$package_fixture/.go-version"
expect_failure "valid release Go version file" "required tool not found: $TAR_BIN" \
	run_package_fixture

package_integrity_fixture="$temp_root/package-integrity-fixture"
mkdir -p "$package_integrity_fixture"
while IFS= read -r -d '' path; do
	mkdir -p "$package_integrity_fixture/$(dirname "$path")"
	cp -p "$repo_root/$path" "$package_integrity_fixture/$path"
done < <(git -C "$repo_root" ls-files -z --cached)
# The compatibility jobs run this fixture with their selected Go patch. Exact
# release-toolchain rejection is covered independently above; these scenarios
# must reach the repository-integrity checks they are designed to exercise.
printf '%s\n' "${installed_go_version#go}" >"$package_integrity_fixture/.go-version"
git -C "$package_integrity_fixture" init -q
git -C "$package_integrity_fixture" config user.name "Release Guard Test"
git -C "$package_integrity_fixture" config user.email "release-guard@example.invalid"
git -C "$package_integrity_fixture" config commit.gpgSign false
git -C "$package_integrity_fixture" add --all
git -C "$package_integrity_fixture" commit -q -m "reviewed packaging fixture"

package_integrity_tools="$temp_root/package-integrity-tools"
mkdir -p "$package_integrity_tools"
printf '%s\n' \
	'#!/usr/bin/env bash' \
	'if [[ "${1:-}" == "--version" ]]; then' \
	'  echo "tar (GNU tar) 1.35"' \
	'  exit 0' \
	'fi' \
	'echo "fixture tar unexpectedly reached" >&2' \
	'exit 97' >"$package_integrity_tools/tar"
printf '%s\n' \
	'#!/usr/bin/env bash' \
	'if [[ "${1:-}" == "--version" ]]; then' \
	'  echo "date (GNU coreutils) 9.5"' \
	'  exit 0' \
	'fi' \
	'echo "fixture date reached after repository integrity checks" >&2' \
	'exit 97' >"$package_integrity_tools/date"
printf '%s\n' \
	'#!/usr/bin/env bash' \
	'echo "fixture sha256sum unexpectedly reached" >&2' \
	'exit 97' >"$package_integrity_tools/sha256sum"
chmod 0755 "$package_integrity_tools/tar" "$package_integrity_tools/date" \
	"$package_integrity_tools/sha256sum"

run_package_integrity_fixture() {
	(
		cd "$package_integrity_fixture"
		env \
			GO="$go_bin" \
			TAR_BIN="$package_integrity_tools/tar" \
			DATE_BIN="$package_integrity_tools/date" \
			SHA256SUM_BIN="$package_integrity_tools/sha256sum" \
			bash scripts/package-release.sh "$@"
	)
}

expect_failure "package source version mismatch" \
	"source version mismatch: requested 'timer-cli $mismatch_version', source is 'timer-cli $source_version'" \
	run_package_integrity_fixture "$mismatch_version"

source_version_probe="$temp_root/source-version-executed"
printf ': >"%s"\n' "$source_version_probe" \
	>>"$package_integrity_fixture/scripts/source-version.sh"
expect_failure "package dirty tracked source" \
	"working tree must be clean: commit or remove tracked and unignored untracked changes before packaging" \
	run_package_integrity_fixture "$source_version"
expect_success "dirty source is rejected before source-version execution" \
	test ! -e "$source_version_probe"
git -C "$package_integrity_fixture" restore --worktree scripts/source-version.sh

printf 'unreviewed untracked source\n' >"$package_integrity_fixture/unreviewed.txt"
expect_failure "package untracked source" \
	"working tree must be clean: commit or remove tracked and unignored untracked changes before packaging" \
	run_package_integrity_fixture "$source_version"
rm -f "$package_integrity_fixture/unreviewed.txt"

mkdir -p "$package_integrity_fixture/bin" "$package_integrity_fixture/dist"
printf 'ignored build output\n' >"$package_integrity_fixture/bin/timer-cli"
printf 'ignored package output\n' >"$package_integrity_fixture/dist/previous-artifact"
printf 'ignored coverage output\n' >"$package_integrity_fixture/coverage.out"
expect_failure "clean package source permits ignored outputs" "source epoch could not be formatted" \
	run_package_integrity_fixture "$source_version"

transferred_fixture="$temp_root/transferred-artifacts"
write_transferred_fixture() {
	rm -rf "$transferred_fixture"
	mkdir -p "$transferred_fixture/dist"
	printf 'Homebrew formula fixture\n' >"$transferred_fixture/dist/timer-cli.rb"
	printf 'AUR fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_aur.tar.gz"
	printf 'darwin amd64 fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_darwin_amd64.tar.gz"
	printf 'darwin arm64 fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_darwin_arm64.tar.gz"
	printf 'linux amd64 fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_linux_amd64.tar.gz"
	printf 'linux arm64 fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_linux_arm64.tar.gz"
	printf 'source fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_source.tar.gz"
	printf 'amd64 snap fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_amd64.snap"
	printf 'arm64 snap fixture\n' >"$transferred_fixture/dist/timer-cli_1.0.0_arm64.snap"
	(
		cd "$transferred_fixture/dist"
		sha256sum \
			timer-cli.rb \
			timer-cli_1.0.0_aur.tar.gz \
			timer-cli_1.0.0_darwin_amd64.tar.gz \
			timer-cli_1.0.0_darwin_arm64.tar.gz \
			timer-cli_1.0.0_linux_amd64.tar.gz \
			timer-cli_1.0.0_linux_arm64.tar.gz \
			timer-cli_1.0.0_source.tar.gz \
			timer-cli_1.0.0_amd64.snap \
			timer-cli_1.0.0_arm64.snap | sort -k2 >SHA256SUMS
	)
}

validate_transferred_fixture() {
	(
		cd "$transferred_fixture"
		# The dynamically sourced artifact validator calls this portability shim.
		# shellcheck disable=SC2317
		find() {
			if [[ "$*" == *"-printf"* ]]; then
				command find dist -mindepth 1 -maxdepth 1 -type f -exec basename {} \;
				return
			fi
			command find "$@"
		}
		export GITHUB_REF_NAME=v1.0.0
		source "$transferred_validator"
	)
}

write_transferred_fixture
expect_success "complete transferred artifact manifest" \
	validate_transferred_fixture

write_transferred_fixture
rm -f "$transferred_fixture/dist/timer-cli_1.0.0_linux_arm64.tar.gz"
expect_failure "missing transferred archive" "timer-cli_1.0.0_linux_arm64.tar.gz" \
	validate_transferred_fixture

write_transferred_fixture
printf 'unexpected regular file\n' >"$transferred_fixture/dist/unexpected.txt"
expect_failure "extra transferred regular file" "unexpected.txt" \
	validate_transferred_fixture

write_transferred_fixture
mkdir "$transferred_fixture/dist/unexpected-directory"
expect_failure "transferred non-regular entry" "release artifact directory contains a non-regular entry" \
	validate_transferred_fixture

write_transferred_fixture
printf 'mutated archive\n' >>"$transferred_fixture/dist/timer-cli_1.0.0_darwin_amd64.tar.gz"
expect_failure "wrong transferred archive digest" "timer-cli_1.0.0_darwin_amd64.tar.gz" \
	validate_transferred_fixture

write_transferred_fixture
grep -v 'timer-cli_1.0.0_linux_arm64.tar.gz' "$transferred_fixture/dist/SHA256SUMS" \
	>"$transferred_fixture/dist/SHA256SUMS.tmp"
mv "$transferred_fixture/dist/SHA256SUMS.tmp" "$transferred_fixture/dist/SHA256SUMS"
expect_failure "missing transferred manifest entry" "timer-cli_1.0.0_linux_arm64.tar.gz" \
	validate_transferred_fixture

write_transferred_fixture
duplicate_sum="$(head -n 1 "$transferred_fixture/dist/SHA256SUMS")"
printf '%s\n' "$duplicate_sum" >>"$transferred_fixture/dist/SHA256SUMS"
expect_failure "duplicate transferred manifest entry" "timer-cli.rb" \
	validate_transferred_fixture

write_transferred_fixture
printf '%064d  %s\n' 0 timer-cli_1.0.0_freebsd_amd64.tar.gz >>"$transferred_fixture/dist/SHA256SUMS"
expect_failure "unexpected transferred manifest archive" "timer-cli_1.0.0_freebsd_amd64.tar.gz" \
	validate_transferred_fixture

ref_repo="$temp_root/ref-repo"
git init -q "$ref_repo"
git -C "$ref_repo" config user.name "Release Guard Test"
git -C "$ref_repo" config user.email "release-guard@example.invalid"
git -C "$ref_repo" config commit.gpgSign false
git -C "$ref_repo" config tag.gpgSign false
git -C "$ref_repo" branch -m main
printf 'reviewed release source\n' >"$ref_repo/source.txt"
git -C "$ref_repo" add source.txt
git -C "$ref_repo" commit -q -m "initial main commit"
git -C "$ref_repo" tag -a v1.0.0 -m "annotated on-main release"
git -C "$ref_repo" tag v1.0.1
git -C "$ref_repo" switch -q -c off-main
printf 'unreviewed release source\n' >>"$ref_repo/source.txt"
git -C "$ref_repo" commit -q -am "off-main commit"
git -C "$ref_repo" tag -a v1.0.2 -m "annotated off-main release"
git -C "$ref_repo" switch -q main
blob_object="$(printf 'not a commit\n' | git -C "$ref_repo" hash-object -w --stdin)"
git -C "$ref_repo" tag -a v1.0.3 "$blob_object" -m "annotated non-commit tag"

verify_release_ref() {
	(
		cd "$ref_repo"
		bash "$script_dir/verify-release-ref.sh" "$@"
	)
}

expect_success "annotated on-main release tag" \
	verify_release_ref refs/tags/v1.0.0 main
expect_failure "lightweight release tag" "tag must be annotated" \
	verify_release_ref refs/tags/v1.0.1 main
expect_failure "annotated off-main release tag" "is not reachable from trusted main ref 'main'" \
	verify_release_ref refs/tags/v1.0.2 main
expect_failure "annotated non-commit release tag" "annotated tag does not peel to a commit" \
	verify_release_ref refs/tags/v1.0.3 main
expect_failure "nonexistent release tag" "tag ref does not exist" \
	verify_release_ref refs/tags/v9.9.9 main
expect_failure "nonexistent trusted main ref" "trusted main ref does not exist or is not a commit" \
	verify_release_ref refs/tags/v1.0.0 origin/main
expect_failure "malformed release tag" "tag ref must match 'refs/tags/vMAJOR.MINOR.PATCH'" \
	verify_release_ref refs/tags/release-1.0.0 main
expect_failure "option-like release tag" "tag ref must match 'refs/tags/vMAJOR.MINOR.PATCH'" \
	verify_release_ref --verify main
expect_failure "option-like trusted main ref" "trusted main ref has invalid format" \
	verify_release_ref refs/tags/v1.0.0 --verify

for valid_version in 0.0.0 1.0.0 10.20.30; do
	expect_success "valid version $valid_version" \
		bash "$script_dir/validate-version.sh" "$valid_version"
done
for invalid_version in 01.0.0 1.01.0 1.0.01 1.0 v1.0.0 1.0.0-rc.1 1.0.0+build " 1.0.0" "1.0.0 "; do
	expect_failure "invalid version $invalid_version" "VERSION must be a stable semantic version" \
		bash "$script_dir/validate-version.sh" "$invalid_version"
done
expect_failure "package malformed version" "VERSION must be a stable semantic version" \
	bash "$script_dir/package-release.sh" "1.0"
expect_failure "verifier malformed version" "VERSION must be a stable semantic version" \
	bash "$script_dir/verify-release.sh" "1.0"
expect_failure "package leading-zero version" "package-release: VERSION must be a stable semantic version" \
	bash "$script_dir/package-release.sh" "01.0.0"
expect_failure "verifier leading-zero version" "verify-release: VERSION must be a stable semantic version" \
	bash "$script_dir/verify-release.sh" "1.01.0"
expect_failure "package repository output" "output directory must not be the repository root" \
	bash "$script_dir/package-release.sh" "$source_version" "."
expect_failure "package filesystem-root output" "output directory must not be the repository root" \
	bash "$script_dir/package-release.sh" "$source_version" "/"
expect_failure "verifier repository export" "export directory must not be the repository root" \
	bash "$script_dir/verify-release.sh" "$source_version" "."
expect_failure "verifier filesystem-root export" "export directory must not be the repository root" \
	bash "$script_dir/verify-release.sh" "$source_version" "/"
expect_failure "source version mismatch" "source version mismatch: expected 'timer-cli $mismatch_version', got 'timer-cli $source_version'" \
	env GO="$go_bin" bash "$script_dir/verify-release.sh" "$mismatch_version"

packaging_fixture="$temp_root/packaging-templates"
mkdir -p "$packaging_fixture"
homebrew_fixture="$packaging_fixture/timer-cli.rb.tmpl"
aur_pkgbuild_fixture="$packaging_fixture/PKGBUILD.tmpl"
aur_srcinfo_fixture="$packaging_fixture/SRCINFO.tmpl"
snapcraft_fixture="$packaging_fixture/snapcraft.yaml.tmpl"

reset_packaging_fixtures() {
	cp "$repo_root/packaging/homebrew/timer-cli.rb.tmpl" "$homebrew_fixture"
	cp "$repo_root/packaging/aur/PKGBUILD.tmpl" "$aur_pkgbuild_fixture"
	cp "$repo_root/packaging/aur/SRCINFO.tmpl" "$aur_srcinfo_fixture"
	cp "$repo_root/packaging/snap/snapcraft.yaml.tmpl" "$snapcraft_fixture"
}

validate_packaging_fixtures() {
	env \
		HOMEBREW_TEMPLATE="$homebrew_fixture" \
		AUR_PKGBUILD_TEMPLATE="$aur_pkgbuild_fixture" \
		AUR_SRCINFO_TEMPLATE="$aur_srcinfo_fixture" \
		SNAPCRAFT_TEMPLATE="$snapcraft_fixture" \
		bash "$script_dir/validate-packaging-templates.sh"
}

reset_packaging_fixtures
expect_success "reviewed packaging templates" validate_packaging_fixtures

reset_packaging_fixtures
sed '1s/^class TimerCli < Formula$/# typed: strict/' "$homebrew_fixture" >"$homebrew_fixture.tmp"
mv "$homebrew_fixture.tmp" "$homebrew_fixture"
expect_failure "Homebrew formula header drift" "Homebrew template must begin with the formula class declaration" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed '1s/^# Maintainer: Burak Ozdemir$/# Maintainer: Unknown/' "$aur_pkgbuild_fixture" >"$aur_pkgbuild_fixture.tmp"
mv "$aur_pkgbuild_fixture.tmp" "$aur_pkgbuild_fixture"
expect_failure "AUR maintainer attribution drift" "AUR PKGBUILD template must begin with the non-sensitive maintainer attribution" \
	validate_packaging_fixtures

reset_packaging_fixtures
printf '# Maintainer: Burak Ozdemir\n' >>"$aur_pkgbuild_fixture"
expect_failure "duplicate AUR maintainer attribution" "AUR PKGBUILD template must contain the maintainer attribution exactly once" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed 's#https://github.com/onlinealarmkur/#https://example.com/#' "$homebrew_fixture" >"$homebrew_fixture.tmp"
mv "$homebrew_fixture.tmp" "$homebrew_fixture"
expect_failure "Homebrew source host drift" "Homebrew template must use the exact generated source archive URL" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed 's/@SOURCE_SHA256@/0123456789abcdef/' "$homebrew_fixture" >"$homebrew_fixture.tmp"
mv "$homebrew_fixture.tmp" "$homebrew_fixture"
expect_failure "Homebrew checksum placeholder drift" "Homebrew template must use the generated source checksum exactly once" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed 's/"-mod=vendor"/"-mod=readonly"/' "$homebrew_fixture" >"$homebrew_fixture.tmp"
mv "$homebrew_fixture.tmp" "$homebrew_fixture"
expect_failure "Homebrew vendoring drift" "Homebrew template must build timer-cli from vendored dependencies" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed '/pkgshare.install "LICENSE", "THIRD_PARTY_LICENSES"/d' "$homebrew_fixture" >"$homebrew_fixture.tmp"
mv "$homebrew_fixture.tmp" "$homebrew_fixture"
expect_failure "Homebrew third-party notices removed" "Homebrew template must install project and third-party license notices" \
	validate_packaging_fixtures

reset_packaging_fixtures
printf '  sha256 "@SOURCE_SHA256@"\n' >>"$homebrew_fixture"
expect_failure "duplicate Homebrew checksum" "Homebrew template must use the generated source checksum exactly once" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed 's#https://github.com/onlinealarmkur/#https://example.com/#' "$aur_pkgbuild_fixture" >"$aur_pkgbuild_fixture.tmp"
mv "$aur_pkgbuild_fixture.tmp" "$aur_pkgbuild_fixture"
expect_failure "AUR source host drift" "AUR PKGBUILD template must use the exact generated source archive URL" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed 's/@SOURCE_SHA256@/not-a-checksum/' "$aur_srcinfo_fixture" >"$aur_srcinfo_fixture.tmp"
mv "$aur_srcinfo_fixture.tmp" "$aur_srcinfo_fixture"
expect_failure "AUR checksum placeholder drift" "AUR .SRCINFO template must use the generated source checksum" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed '/install -Dm644 THIRD_PARTY_LICENSES/d' "$aur_pkgbuild_fixture" >"$aur_pkgbuild_fixture.tmp"
mv "$aur_pkgbuild_fixture.tmp" "$aur_pkgbuild_fixture"
expect_failure "AUR third-party notices removed" "AUR PKGBUILD template must install third-party license notices" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed 's/confinement: strict/confinement: devmode/' "$snapcraft_fixture" >"$snapcraft_fixture.tmp"
mv "$snapcraft_fixture.tmp" "$snapcraft_fixture"
expect_failure "Snap confinement drift" "Snapcraft template must use strict confinement" \
	validate_packaging_fixtures

reset_packaging_fixtures
printf '    plugs: [network]\n' >>"$snapcraft_fixture"
expect_failure "Snap runtime interface added" "Snapcraft template must not request runtime interfaces" \
	validate_packaging_fixtures

reset_packaging_fixtures
sed '/THIRD_PARTY_LICENSES: share\/doc\/timer-cli\/THIRD_PARTY_LICENSES/d' "$snapcraft_fixture" >"$snapcraft_fixture.tmp"
mv "$snapcraft_fixture.tmp" "$snapcraft_fixture"
expect_failure "Snap third-party notices removed" "Snapcraft template must install third-party license notices" \
	validate_packaging_fixtures

reset_packaging_fixtures
mv "$homebrew_fixture" "$homebrew_fixture.real"
ln -s "$(basename "$homebrew_fixture.real")" "$homebrew_fixture"
expect_failure "symlinked packaging template" "packaging template must be a regular non-symlink file" \
	validate_packaging_fixtures

echo "release guard tests passed ($guard_count cases)"

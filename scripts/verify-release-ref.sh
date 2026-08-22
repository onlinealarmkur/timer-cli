#!/usr/bin/env bash

set -euo pipefail

die() {
	echo "verify-release-ref: $*" >&2
	exit 1
}

tag_ref="${1:-}"
trusted_ref="${2:-}"

(( $# == 2 )) || die "usage: verify-release-ref.sh <tag-ref> <trusted-main-ref>"
[[ "$tag_ref" =~ ^refs/tags/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || \
	die "tag ref must match 'refs/tags/vMAJOR.MINOR.PATCH': '$tag_ref'"
[[ -n "$trusted_ref" ]] || die "trusted main ref is required"
[[ "$trusted_ref" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ && "$trusted_ref" != *..* && "$trusted_ref" != *//* ]] || \
	die "trusted main ref has invalid format: '$trusted_ref'"
command -v git >/dev/null 2>&1 || die "required tool not found: git"

tag_object="$(git rev-parse --verify --quiet "$tag_ref^{object}")" || die "tag ref does not exist: '$tag_ref'"
tag_type="$(git cat-file -t "$tag_object")" || die "could not inspect tag ref: '$tag_ref'"
[[ "$tag_type" == "tag" ]] || die "tag must be annotated: '$tag_ref' resolves to a $tag_type object"

tag_commit="$(git rev-parse --verify --quiet "$tag_ref^{commit}")" || die "annotated tag does not peel to a commit: '$tag_ref'"
trusted_commit="$(git rev-parse --verify --quiet "$trusted_ref^{commit}")" || die "trusted main ref does not exist or is not a commit: '$trusted_ref'"

git merge-base --is-ancestor "$tag_commit" "$trusted_commit" || \
	die "tag commit $tag_commit is not reachable from trusted main ref '$trusted_ref'"

echo "verified annotated release tag '$tag_ref' is reachable from '$trusted_ref'"

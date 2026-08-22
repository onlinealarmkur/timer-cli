#!/usr/bin/env bash

set -euo pipefail

version="${1:-}"
if (( $# != 1 )) || [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
	echo "VERSION must be a stable semantic version such as 1.0.0" >&2
	exit 1
fi

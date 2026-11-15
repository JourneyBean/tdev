#!/bin/sh
# Run from the source root. VERSION remains the release's base version.
set -eu

base=${1:?usage: build-version.sh BASE_VERSION}
if ! commit=$(git rev-parse --verify HEAD 2>/dev/null); then
    printf '%s-dev.unknown\n' "$base"
    exit 0
fi
short=$(git rev-parse --short=12 HEAD)
status=$(git status --porcelain --untracked-files=normal)
tag=$(git rev-parse --verify "refs/tags/v$base^{commit}" 2>/dev/null || true)
if [ "$tag" = "$commit" ] && [ -z "$status" ]; then
    printf '%s\n' "$base"
else
    suffix=
    if [ -n "$status" ]; then
        suffix=.dirty
    fi
    printf '%s-git.%s%s\n' "$base" "$short" "$suffix"
fi

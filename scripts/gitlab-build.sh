#!/bin/sh
# This script runs inside the disposable GitLab build container.
set +x
set -eu
umask 077
[ "${CI_COMMIT_REF_PROTECTED:-false}" = true ] || { printf '%s\n' 'Release builds require a protected tag.' >&2; exit 2; }
[ "${CI_DEBUG_TRACE:-false}" != true ] || { printf '%s\n' 'Disable CI_DEBUG_TRACE for signed builds.' >&2; exit 2; }
: "${CI_COMMIT_TAG:?version tag required}"
: "${CI_PROJECT_URL:?GitLab project URL required}"
: "${TDEV_SIGNING_KEY:?protected File variable containing the existing PKCS8 Ed25519 key required}"
printf '%s\n' "$CI_COMMIT_TAG" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || { printf '%s\n' 'Tag must be vMAJOR.MINOR.PATCH.' >&2; exit 2; }
version=${CI_COMMIT_TAG#v}
make_version=$(awk '$1 == "VERSION" && $2 == "?=" { print $3 }' Makefile)
source_version=$(sed -n 's/^var Version = "\([^"]*\)"$/\1/p' internal/cli/cli.go)
[ "$version" = "$make_version" ] && [ "$version" = "$source_version" ] || { printf '%s\n' 'Tag, Makefile and source versions must agree.' >&2; exit 2; }
[ -s "docs/releases/$CI_COMMIT_TAG.md" ] || { printf '%s\n' 'Release notes for this tag are required.' >&2; exit 2; }
signing_dir=$(mktemp -d)
trap 'rm -rf -- "$signing_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
install -m 600 "$TDEV_SIGNING_KEY" "$signing_dir/release.pem"
openssl pkey -in "$signing_dir/release.pem" -pubout -out "$signing_dir/release.pub" 2>/dev/null
cmp -s "$signing_dir/release.pub" docs/release.pub || { printf '%s\n' 'Signing key differs from docs/release.pub.' >&2; exit 2; }
export TDEV_RELEASE_URL="${CI_PROJECT_URL%/}/-/releases/$CI_COMMIT_TAG/downloads"
export TDEV_UPGRADE_URL="${CI_PROJECT_URL%/}/-/releases/permalink/latest/downloads"
TDEV_SIGNING_KEY="$signing_dir/release.pem" make release VERSION="$version"

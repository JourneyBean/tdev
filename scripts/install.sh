#!/bin/sh
# Bootstrap without executing downloaded code until a trusted signature and hash are verified.
set -eu
umask 077
release_dir= release_url= public_key= version= destination="${HOME}/.local/bin/tdev"
config_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/tdev"
usage() { printf '%s\n' 'Usage: sh install.sh (--release-dir DIR | --release-url HTTPS_URL) --public-key TRUSTED_PEM [--version VERSION] [--destination PATH] [--config-dir DIR]'; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --release-dir|--release-url|--public-key|--version|--destination|--config-dir)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      case "$1" in
        --release-dir) release_dir=$2 ;; --release-url) release_url=$2 ;;
        --public-key) public_key=$2 ;; --version) version=$2 ;;
        --destination) destination=$2 ;; --config-dir) config_dir=$2 ;;
      esac
      shift 2 ;;
    *) usage >&2; exit 2 ;;
  esac
done
[ -n "$public_key" ] && [ -f "$public_key" ] || { printf '%s\n' 'An independently trusted public key file is required.' >&2; exit 2; }
if [ -n "$release_dir" ]; then
  [ -z "$release_url" ] || { usage >&2; exit 2; }
else
  case "$release_url" in https://*) ;; *) usage >&2; exit 2 ;; esac
fi
openssl_bin=${OPENSSL:-openssl}
case "$("$openssl_bin" version)" in 'OpenSSL 3.'*|'OpenSSL 4.'*) ;; *) printf '%s\n' 'Bootstrap signature verification requires OpenSSL 3+ (set OPENSSL to its path).' >&2; exit 7 ;; esac
case "$(uname -s)" in Linux) goos=linux ;; Darwin) goos=darwin ;; *) printf '%s\n' 'Unsupported OS.' >&2; exit 2 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) printf '%s\n' 'Unsupported architecture.' >&2; exit 2 ;; esac
asset=tdev-$goos-$arch
stage=$(mktemp -d "${TMPDIR:-/tmp}/tdev-bootstrap.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
fetch() {
  if [ -n "$release_dir" ]; then
    cat "$release_dir/$1" > "$stage/$1"
  else
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 120 --max-filesize 268435456 "${release_url%/}/$1" -o "$stage/$1"
  fi
}
fetch SHA256SUMS
fetch SHA256SUMS.sig
"$openssl_bin" pkeyutl -verify -pubin -inkey "$public_key" -rawin -in "$stage/SHA256SUMS" -sigfile "$stage/SHA256SUMS.sig" >/dev/null
expected= matches=0
while read -r digest filename; do
  if [ "$filename" = "$asset" ]; then expected=$digest; matches=$((matches + 1)); fi
done < "$stage/SHA256SUMS"
[ "$matches" -eq 1 ] && [ "${#expected}" -eq 64 ] || { printf '%s\n' 'Release checksum entry is missing or ambiguous.' >&2; exit 2; }
case "$expected" in *[!0-9a-f]*) printf '%s\n' 'Invalid SHA-256.' >&2; exit 2 ;; esac
fetch "$asset"
actual=$("$openssl_bin" dgst -sha256 "$stage/$asset")
actual=${actual##* }
[ "$actual" = "$expected" ] || { printf '%s\n' 'Release binary SHA-256 mismatch.' >&2; exit 2; }
fetch manifest.json
fetch manifest.sig
cat "$public_key" > "$stage/release.pub"
chmod 700 "$stage/$asset"
# The candidate is now authenticated. It independently verifies the manifest and performs atomic installation.
"$stage/$asset" --config-dir "$config_dir" --json local binary install --release-dir "$stage" --public-key "$stage/release.pub" --version "$version" --destination "$destination" --source-url "$release_url"
printf 'Installed %s. Add its directory to PATH if necessary.\n' "$destination"

#!/bin/sh
# Release packaging embeds the trusted public key and per-platform binary hashes.
set -eu
umask 077
release_version='@TDEV_VERSION@'
embedded_public_key='@TDEV_PUBLIC_KEY@'
trusted_checksums='@TDEV_CHECKSUMS@'
embedded_release_url='@TDEV_RELEASE_URL@'
embedded_upgrade_url='@TDEV_UPGRADE_URL@'
release_dir= release_url= public_key= version= shell=auto
destination="${HOME}/.local/bin/tdev"
config_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/tdev"
modify_path=auto custom_destination=no default_source=no
completion=auto
usage() {
  printf '%s\n' 'Install tdev for macOS/Linux. No sudo required.' \
    'Usage: sh install.sh [--version VERSION] [--no-modify-path | --add-path] [--shell auto|bash|zsh|fish|sh]' \
    '       [--destination PATH] [--config-dir DIR]' \
    '       [--completion auto|none|bash|zsh|all | --install-completion | --no-completion]' \
    '       [--release-dir DIR | --release-url HTTPS_URL] [--public-key TRUSTED_PEM]'
}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --no-modify-path) modify_path=no; shift ;;
    --add-path) modify_path=yes; shift ;;
    --install-completion) completion=yes; shift ;;
    --no-completion) completion=none; shift ;;
    --release-dir|--release-url|--public-key|--version|--destination|--config-dir|--shell|--completion)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      case "$1" in
        --release-dir) release_dir=$2 ;; --release-url) release_url=$2 ;;
        --public-key) public_key=$2 ;; --version) version=$2 ;;
        --destination) destination=$2; custom_destination=yes ;;
        --config-dir) config_dir=$2 ;; --shell) shell=$2 ;;
        --completion) completion=$2 ;;
      esac
      shift 2 ;;
    *) usage >&2; exit 2 ;;
  esac
done
case "$completion" in auto|yes|none|bash|zsh|all) ;; *) printf '%s\n' 'Completion must be auto, none, bash, zsh or all.' >&2; exit 2 ;; esac
case "$destination" in '~/'*) destination="$HOME/${destination#\~/}" ;; esac
case "$release_version" in @*) packaged=no ;; *) packaged=yes ;; esac
if [ -z "$version" ] && [ "$packaged" = yes ]; then version=$release_version; fi
if [ -n "$release_dir" ]; then
  [ -z "$release_url" ] || { usage >&2; exit 2; }
elif [ -z "$release_url" ]; then
  [ "$packaged" = yes ] || { printf '%s\n' 'Use the installer attached to a Release, or provide --release-dir/--release-url and --public-key.' >&2; exit 2; }
  case "$version" in *[!0-9A-Za-z.-]*|'') printf '%s\n' 'Invalid version.' >&2; exit 2 ;; esac
  if [ -n "$embedded_release_url" ]; then
    release_url=$embedded_release_url
  else
    release_url="https://github.com/JourneyBean/tdev/releases/download/v${version#v}"
  fi
  default_source=yes
fi
if [ -n "$release_url" ]; then
  case "$release_url" in https://*) ;; *) printf '%s\n' 'Release URL must use HTTPS.' >&2; exit 2 ;; esac
fi
case "$(uname -s)" in Linux) goos=linux ;; Darwin) goos=darwin ;; *) printf '%s\n' 'Unsupported OS: macOS and Linux are supported.' >&2; exit 2 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) printf '%s\n' 'Unsupported architecture: amd64 and arm64 are supported.' >&2; exit 2 ;; esac
asset=tdev-$goos-$arch
stage=$(mktemp -d "${TMPDIR:-/tmp}/tdev-bootstrap.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
custom_key=no
if [ -n "$public_key" ]; then
  [ -f "$public_key" ] || { printf '%s\n' 'Public key file does not exist.' >&2; exit 2; }
  custom_key=yes
elif [ "$packaged" = yes ]; then
  printf '%s\n' "$embedded_public_key" > "$stage/release.pub"
  public_key="$stage/release.pub"
else
  printf '%s\n' 'An independently trusted public key file is required for a custom release.' >&2; exit 2
fi
fetch() {
  if [ -n "$release_dir" ]; then
    cat "$release_dir/$1" > "$stage/$1"
  else
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --retry 2 --connect-timeout 15 --max-time 120 --max-filesize 268435456 "${release_url%/}/$1" -o "$stage/$1"
  fi
}
expected=
if [ "$packaged" = yes ] && [ "$custom_key" = no ] && [ "${version#v}" = "$release_version" ]; then
  expected=$(printf '%s\n' "$trusted_checksums" | awk -v asset="$asset" '$2 == asset {print $1}')
else
  # Custom/offline releases retain signature-based bootstrap verification.
  openssl_bin=${OPENSSL:-openssl}
  case "$("$openssl_bin" version)" in 'OpenSSL 3.'*|'OpenSSL 4.'*) ;; *) printf '%s\n' 'Custom release bootstrap requires OpenSSL 3+ (set OPENSSL to its path).' >&2; exit 7 ;; esac
  fetch SHA256SUMS
  fetch SHA256SUMS.sig
  "$openssl_bin" pkeyutl -verify -pubin -inkey "$public_key" -rawin -in "$stage/SHA256SUMS" -sigfile "$stage/SHA256SUMS.sig" >/dev/null
  expected=$(awk -v asset="$asset" '$2 == asset {print $1}' "$stage/SHA256SUMS")
fi
[ "${#expected}" -eq 64 ] || { printf '%s\n' 'Release checksum entry is missing or ambiguous.' >&2; exit 2; }
case "$expected" in *[!0-9a-f]*) printf '%s\n' 'Invalid SHA-256.' >&2; exit 2 ;; esac
printf 'Downloading tdev %s (%s/%s)…\n' "$version" "$goos" "$arch"
fetch "$asset"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$stage/$asset")
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$stage/$asset")
else
  printf '%s\n' 'sha256sum or shasum is required.' >&2; exit 7
fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || { printf '%s\n' 'Release binary SHA-256 mismatch.' >&2; exit 2; }
fetch manifest.json
fetch manifest.sig
if [ "$public_key" != "$stage/release.pub" ]; then cat "$public_key" > "$stage/release.pub"; fi
chmod 700 "$stage/$asset"
remember_url=$release_url
if [ "$default_source" = yes ]; then
  remember_url=${embedded_upgrade_url:-https://github.com/JourneyBean/tdev/releases/latest/download}
fi
# Only authenticated bytes execute; the candidate verifies the signed manifest again.
"$stage/$asset" --config-dir "$config_dir" --json api binary install --release-dir "$stage" --public-key "$stage/release.pub" --version "$version" --destination "$destination" --source-url "$remember_url"
printf '\nInstalled %s.\n' "$destination"
if [ "$modify_path" = auto ]; then
  if [ "$custom_destination" = no ]; then modify_path=yes; else modify_path=no; fi
fi
if [ "$modify_path" = yes ]; then
  "$destination" --config-dir "$config_dir" path --write --shell "$shell" --directory "$(dirname "$destination")" || {
    printf 'Binary installed. PATH setup needs attention; run: "%s" path --write --shell bash\n' "$destination" >&2
    exit 9
  }
else
  printf 'To configure PATH later, run: "%s" path --write\n' "$destination"
fi
completion_shell=$shell
if [ "$completion_shell" = auto ]; then completion_shell=${SHELL:-}; completion_shell=${completion_shell##*/}; fi
case "$completion_shell" in bash|zsh) ;; *) completion_shell=all ;; esac
case "$completion" in
  auto)
    # A curl | sh installer reads its answer from the controlling terminal.
    if ( : </dev/tty ) 2>/dev/null; then
      if [ "$completion_shell" = all ]; then completion_label='bash and zsh'; else completion_label=$completion_shell; fi
      printf '\nConfigure %s command completion? [Y/n] ' "$completion_label" >/dev/tty
      if IFS= read -r completion_answer </dev/tty; then
        case "$completion_answer" in ''|y|Y|yes|YES) completion=$completion_shell ;; *) completion=none ;; esac
      else
        completion=none
      fi
    else
      completion=none
      printf 'Configure completion later: "%s" completion install --shell %s\n' "$destination" "$completion_shell"
    fi ;;
  yes) completion=$completion_shell ;;
esac
if [ "$completion" != none ]; then
  "$destination" --config-dir "$config_dir" completion install --shell "$completion" || {
    printf 'Binary installed. Completion setup needs attention; run: "%s" completion install --shell %s\n' "$destination" "$completion" >&2
    exit 9
  }
fi
printf '\nStart with: tdev setup\n'

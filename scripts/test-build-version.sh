#!/bin/sh
set -eu

script=$(pwd)/scripts/build-version.sh
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
# Keep fixture commits independent of developer Git configuration and hooks.
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
cd "$fixture"
expect() {
    actual=$(sh "$script" 0.8.0)
    if [ "$actual" != "$1" ]; then
        printf 'expected %s; got %s\n' "$1" "$actual" >&2
        exit 1
    fi
}
expect 0.8.0-dev.unknown
git init -q
expect 0.8.0-dev.unknown
git config user.name 'Build Test'
git config user.email 'build-test@example.invalid'
printf '/bin/\n/dist/\n' > .gitignore
printf 'source\n' > source.txt
git add .
git -c commit.gpgsign=false commit -qm initial
short=$(git rev-parse --short=12 HEAD)
expect "0.8.0-git.$short"
git -c tag.gpgsign=false tag v0.8.0
expect 0.8.0
mkdir bin dist
touch bin/tdev dist/tdev-linux-amd64
expect 0.8.0
printf 'modified\n' >> source.txt
expect "0.8.0-git.$short.dirty"
git add source.txt
expect "0.8.0-git.$short.dirty"
git -c commit.gpgsign=false commit -qm change
short=$(git rev-parse --short=12 HEAD)
expect "0.8.0-git.$short"
touch new-source.txt
expect "0.8.0-git.$short.dirty"
printf 'Build version tests passed\n'

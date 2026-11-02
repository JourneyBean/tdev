# tdev development guide

This is the Go implementation of the tdev product design in `docs/design.md`.

- Follow the workspace's development and operation rules. For remote development,
  edit through a verified SSHFS mount and run builds and tests on the target host.
- Keep system OpenSSH as the transport; do not add an SSH server or remote daemon.
- Project definitions belong to the remote machine. Connection preferences belong
  to the client. Do not capture environment variables, history or credentials.
- Local machine interfaces must be noninteractive, return protocol JSON when
  requested, and distinguish configuration errors from transport failures.
- Preserve existing worktrees and existing tmux sessions. Tests use private
  temporary configuration roots and unique tmux sockets with `/dev/null` config.
- Do not install over `devc-remote`, change SSH keys or run sudo as part of a test.
- Required checks: `go test -race ./...`, `go vet ./...`, `gofmt -l cmd internal`,
  `git diff --check`, and `make cross` after platform-sensitive changes.
- Run real tmux integration tests and record their results and coverage limits.
- Record implementation decisions in `docs/development.md` and validation in
  `docs/acceptance.md`.

## Public repository hygiene

- Treat all tracked files, commit metadata, tags, releases, CI logs and artifacts
  as public. Keep infrastructure details in a separate private operations store.
- Use generic examples such as `developer`, `dev-host`, `server.example.invalid`
  and `/home/developer/projects/demo`. Exclude real machine names, SSH aliases,
  connection ports, internal domains/IPs, personal filesystem paths and task IDs.
- Keep credentials, private keys, tokens, `.env` files and live SSH/deployment
  configuration outside the repository. Generate test identities in temporary
  directories and clean them up after use.
- Before committing or publishing, review the diff and tracked filenames, scan
  for secrets and internal environment identifiers, and inspect release/CI output.
  A new public repository or rewritten history requires a scan of all reachable
  commits and tags. Report findings using locations and categories, with values
  redacted. Use a public noreply address for Git author and tagger metadata.
- If published data needs removal, coordinate history, tags, release archives,
  CI artifacts and platform cleanup with the user. History rewrites and force
  pushes require explicit authorization and exact expected remote revisions.

## Documentation style

- State behavior, results and requirements directly. Use concise, concrete prose.
- Keep necessary compatibility limits and safety constraints next to the behavior
  they affect. Consolidate validation gaps in `docs/acceptance.md`.
- Avoid repeated disclaimers and defensive contrasts such as “不是…”, “不代表…”,
  “不等于…” and “不会…”. Prefer a positive description; retain a prohibition when
  it defines an essential product or security requirement.
- Remove task handoff chatter, local recovery notes and obsolete release status.
  Public documentation should explain the product to a new contributor.

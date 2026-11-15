---
name: tdev
description: Manage tdev local and remote development workspaces, tmux projects and scratch sessions, SSH connections, port forwards, file transfers and mounts. Use when the user asks to operate or troubleshoot tdev.
---

# tdev workspaces

Use the installed `tdev` CLI. Start with `tdev version --json` and the relevant command's `--help`; the installed version is the authority for flags and capabilities. If the executable is missing from PATH, check `~/.local/bin/tdev`. Explain a missing installation rather than downloading or upgrading without a user request.

## Choose the machine and interface

- User commands route through saved client connections. Use explicit `local/project` or `server/project` targets and explicit `--server` filters in automation. Run `tdev --json ls` to discover projects, scratch sessions and connection failures.
- `tdev api` always operates on the machine where the process runs. It is the noninteractive machine interface, not a shortcut for selecting a remote server. Use routed user commands for registered servers; use API commands directly when already working on the intended host.
- Use `--json` for structured responses. Read `ok`, `data` and `error`, as well as the process exit code. Partial success (exit 9) retains useful results in `error.details`; inspect these before retrying a mutation. Configuration errors and SSH transport failures require different fixes.
- Use explicit arguments and structured stdin to avoid interactive wizards. `-y` accepts confirmations; add it only for changes the user has authorized. Interactive shell attachment needs a real terminal.

## Inspect and operate

```sh
tdev --json ls --server local
tdev --json show dev/demo
tdev --json ps --server dev
tdev --json doctor dev/demo
```

`show` returns the saved definition, client overrides and effective configuration. Before replacing a project, read its current definition and revision. For a full replacement, pass the edited `definition` object to `tdev --json edit dev/demo --stdin --expected-revision N`; preserve unrelated fields and handle revision conflicts by reading again.

Common workflows (check `--help` for additional options):

- Create a project in an existing directory: `tdev --json create local/demo --directory /home/developer/projects/demo -y`. For a remote project, use `dev/demo` and a directory on that server.
- Open an existing workspace in a user terminal: `tdev open dev/demo`. For background port mappings only, use `tdev --json open dev/demo --only forward`.
- Create a scratch session without attaching: `tdev --json scratch dev --name debug`. Resume it in a terminal with `tdev open dev/debug`.
- Start a temporary server forward: `tdev --json forward dev -L 3000:localhost:8000`. It runs in the background by default; inspect it through `ls` and `ps`.
- For file copies, moves and SSHFS mounts, inspect `tdev cp --help`, `tdev mv --help` or `tdev mount --help`. Confirm source/destination machine and overwrite intent before a destructive change.

Keep project definitions on their owning machine and client connection preferences/overrides on the client. Retain existing tmux sessions and worktrees. Use tdev's managed operations rather than killing tmux servers or editing private runtime state. A detached terminal or SSH disconnect normally leaves the session running; stopping a project terminates its processes.

## Diagnose failures

Inspect the specific target with `doctor`, and connections/forwards with `ps`. Classify configuration, missing dependency, transport and protocol compatibility errors before choosing a repair. Use `--debug` only when diagnostic logs are needed, and summarize relevant findings without copying credentials, environment dumps or shell history.

Use system OpenSSH and existing SSH configuration. Connection repair does not authorize changing SSH keys, disabling host verification, installing a remote daemon or upgrading software. After an explicitly requested installation or upgrade, inspect `post_install` hook results; `tdev post-install` retries the installed version's hooks on this host, and `--remote dev` retries on a registered server.

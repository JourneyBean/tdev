---
name: tdev
description: Operate and troubleshoot tdev local and remote development workspaces, project and scratch tmux sessions, TCP forwards, HTTP routes, SSH connections, file transfers and mounts.
---

# tdev workspaces

Use the installed `tdev` CLI. Start with `tdev version --json` and the relevant command's `--help`; the installed version is the authority for flags and capabilities. If the executable is missing from PATH, check `~/.local/bin/tdev`. Explain a missing installation rather than downloading or upgrading without a user request.

## Choose the machine and interface

- User commands route through saved client connections. Use explicit `local/project` or `server/project` targets and explicit `--server` filters in automation.
- `tdev api` operates on the machine where the process runs. Use routed user commands for registered servers; use API commands directly when already working on the intended host. The API is noninteractive, except that session attachment needs a real terminal and explicit attachment identity.
- Use `--json` for structured responses. Read `ok`, `data`, `error` and the exit code. Partial success (exit 9) retains useful results in `error.details`; inspect these before retrying a mutation. Configuration errors and SSH transport failures require different fixes.
- Use explicit arguments and structured stdin to avoid interactive wizards. `-y` accepts confirmations; add it only for changes the user has authorized.

## Choose the right query

| Need | Command | Meaning |
| --- | --- | --- |
| Active work items | `tdev --json ls --server dev` | Projects with active resources, scratch sessions and temporary forward groups. |
| All project definitions | `tdev --json project ls --server dev` | Includes projects that have never been opened. |
| Actual tmux sessions | `tdev --json session ls --server dev` | Project/scratch sessions and attached client counts. |
| TCP listeners | `tdev --json forward ls --server dev` | One row per active mapping, including temporary forwards. |
| HTTP routes | `tdev --json web ls --server dev` | One row per registered Web route. |
| Connections and PIDs | `tdev --json connection ls --server local` | One row per SSH master or carrier process, with its resource users; `ps` is the equivalent shortcut. |
| Saved and effective configuration | `tdev --json show dev/demo` | Definition, client overrides and effective configuration. |

The overview and runtime lists default to active items; `project ls` lists all configured projects. Add `--include-inactive` to include stopped/configured items; `--all` retains its separate meaning of querying every server. A project may have active TCP, Web or mount resources without a tmux session. `ls --type project|scratch|forward` remains a compatibility filter; prefer the explicit query above for new automation.

For resource-list JSON, use each server's `items` for the selected view. The `projects`, `scratches`, `forwards` and `web` arrays preserve the complete underlying snapshot, including configuration and original runtime states; their presence alone does not indicate activity. Presentation status normalizes healthy resources to `running`, while waiting, failure and unknown states remain distinct. Cached session observations from an unreachable server have `unknown` presentation status.

Add `--wide` to a resource list to associate resources with connection IDs and PIDs. `connection ls --verbose` (or `ps --verbose`) expands daemon, socket, allocation and channel details. Connection JSON contains merged `connections` and original per-machine `hosts`. In connection queries, `--server` and the `server` column identify the machine where the processes run; resource references identify their destination server. PIDs must be interpreted on the observed machine. Shared connections appear once and enumerate all resource users.

A detached session can remain running with zero attached clients and no current transport. Missing connection associations or client counts may require a newer daemon/server; inspect version and capabilities before proposing a restart or upgrade. Connection queries preserve stopped daemons.

## Operate the requested resource

Before replacing a project, read `show` and its current revision. For a full replacement, pass the edited `definition` object to `tdev --json edit dev/demo --stdin --expected-revision N`; preserve unrelated fields and handle revision conflicts by reading again.

Common workflows (check `--help` for additional options):

- Create a project in an existing directory: `tdev --json create local/demo --directory /home/developer/projects/demo -y`. For a remote project, use `dev/demo` and a directory on that server.
- Open a workspace in a user terminal: `tdev open dev/demo`. Default open includes shell, TCP and Web; SSHFS is opt-in with `--mount` or `--only sshfs`. Use `--only forward` or `--only web` for background services without opening a session.
- Create a scratch without attaching: `tdev --json scratch dev --name debug`. Resume it with `tdev open dev/debug` in a terminal.
- Start a temporary TCP forward: `tdev --json forward dev -L 3000:localhost:8000`. It runs in the background by default; find its generated name through `forward ls` and stop that group with `tdev stop dev/forward-NAME`.
- Copy files or directory contents with `push` (client to project) and `pull` (project to client), each taking `<project> <source> <destination>`. Confirm endpoints and overwrite intent. The user CLI has no `cp` or `mv` commands.
- Inspect `mount --help` and `unmount --help` for SSHFS operations. Preserve existing worktrees and mounts belonging to other users or clients.

Stop the intended resource: `stop` ends the named project/scratch tmux session or temporary forward group; `web stop server/project` removes that project's HTTP routes and tunnels; `unmount` releases an identity-matching mount. Background TCP/Web resources have their own lifecycle. Stopping a session and stopping all its background services are separate operations. A `daemon stop` affects the supervisor's managed resources and clears recovery registration; scope that action to the user's request.

Keep definitions on their owning machine and client preferences/overrides on the client. Use managed operations rather than killing tmux servers or editing private runtime state. Detach or SSH disconnect normally leaves the session running; stopping a session terminates its foreground processes.

## Diagnose failures

Inspect the specific target with `doctor`; use the resource-specific list for listener/route state and `connection ls` for transport/PID ownership. `running` describes session, listener or carrier lifecycle; application health requires an application check. Classify configuration, dependency, transport and protocol compatibility errors before choosing a repair. Use `--debug` when diagnostic logs are needed, and summarize findings without copying credentials, environment dumps or shell history.

Use system OpenSSH and existing SSH configuration. Connection repair does not authorize changing SSH keys, disabling host verification, installing a remote daemon or upgrading software. After an explicitly requested installation or upgrade, inspect `post_install` hook results; `tdev post-install` retries this host's hooks, and `--remote dev` retries on a registered server. Skill updates use `skill install` with the user's selected targets; preserve locally edited copies reported as conflicts.

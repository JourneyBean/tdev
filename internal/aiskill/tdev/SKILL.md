---
name: tdev
description: Operate or troubleshoot development workspaces managed by tdev over SSH and tmux.
---

# tdev workspaces

Use the installed CLI. Run `tdev --help` to discover commands and `tdev <command> [subcommand] --help` for syntax, defaults and flags. Check `tdev version --json` when capabilities or protocol compatibility matter. If tdev is missing from PATH, check `~/.local/bin/tdev`.

## Where operations run

- User commands route through saved client connections. Use explicit targets such as `local/demo` or `dev/demo`, and scope queries with `--server` where available. Project definitions live on the project host. Connection preferences and overrides live on the client.
- `tdev api --help` describes the machine interface, which operates on the host where it runs. Session attachment needs a real terminal.
- `tdev connection ls --help` is the entry point for transport diagnostics. Connections and PIDs belong to the observed machine. Resource references identify the destination server, and a shared connection can serve several resources.

## Interpret state

- `tdev ls --help` covers work items. Use `tdev project ls --help` for saved definitions, including projects that have never run. Session, TCP forward, Web route and mount lifecycles are independent. A detached session can keep running without an attached client or SSH connection. Stopping a session terminates its foreground processes.
- For resource-list JSON, read each server's `items` for the selected view. The raw snapshot arrays also contain configuration and original runtime states. An unreachable host can yield cached sessions with `unknown` status. A running listener or route still requires an application check to establish health.

## Automate safely

- Use explicit arguments and `--json`. Read the exit code and response together. On partial success (exit 9), inspect `error.details` before retrying. Distinguish configuration errors from SSH transport failures.
- For full project replacement, take the saved `definition` and revision from `tdev show`. Preserve unrelated fields and use the revision guard documented by `tdev edit --help`.

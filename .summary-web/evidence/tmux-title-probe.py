#!/usr/bin/env python3
"""Isolated native tmux/PTY probe; no user configuration or agent hooks."""
import fcntl
import json
import os
import pty
import re
import select
import struct
import subprocess
import tempfile
import termios
import time


def main():
    with tempfile.TemporaryDirectory(prefix="tdev-title-probe-") as root:
        socket = root + "/tmux.sock"
        base = ["tmux", "-S", socket, "-f", "/dev/null"]
        env = {"PATH": os.defpath, "HOME": root, "TERM": "xterm-256color", "LC_ALL": "C"}

        def tm(*args):
            return subprocess.check_output(base + list(args), env=env, stderr=subprocess.STDOUT).decode().strip()

        child = master = None
        results = {"tmux": subprocess.check_output(["tmux", "-V"]).decode().strip(), "stages": []}
        try:
            tm("new-session", "-d", "-s", "demo", "-n", "agent-a", "/bin/sh")
            tm("new-window", "-d", "-t", "demo:1", "-n", "agent-b", "/bin/sh")
            tm("new-window", "-t", "demo:2", "-n", "observer", "/bin/sh")
            tm("set-option", "-t", "demo", "set-titles", "on")
            tm("set-option", "-t", "demo", "set-titles-string", "#{?session_alerts,[!] ,}#S:#W")
            tm("set-option", "-t", "demo", "status-interval", "60")
            tm("set-option", "-t", "demo", "visual-bell", "off")
            tm("set-option", "-t", "demo", "bell-action", "any")
            tm("set-option", "-s", "focus-events", "on")
            tm("set-window-option", "-g", "monitor-bell", "on")
            tm("set-window-option", "-g", "monitor-activity", "off")
            child, master = pty.fork()
            if child == 0:
                os.execvpe("tmux", base + ["attach-session", "-t", "demo"], env)
            fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))

            def drain(seconds=0.5):
                data = b""
                until = time.monotonic() + seconds
                while time.monotonic() < until:
                    ready, _, _ = select.select([master], [], [], max(0, until - time.monotonic()))
                    if ready:
                        try:
                            data += os.read(master, 65536)
                        except OSError:
                            break
                return data

            def record(name, data):
                titles = [v.decode(errors="replace") for v in re.findall(rb"\x1b\][012];(.*?)(?:\x07|\x1b\\)", data, re.S)]
                results["stages"].append({"stage": name, "titles_emitted": titles,
                    "alerts": tm("display-message", "-p", "-t", "demo", "#{session_alerts}"),
                    "windows": tm("list-windows", "-t", "demo", "-F", "#{window_index}:#{window_name} bell=#{window_bell_flag} activity=#{window_activity_flag}"),
                    "client_focus": tm("list-clients", "-F", "#{client_termname} focus=#{client_flags}")})

            def bell(target):
                tm("send-keys", "-t", target, "printf '\\007'", "Enter")
                return drain()

            record("initial", drain(1))
            record("agent_a_bell", bell("demo:0"))
            record("agent_b_bell", bell("demo:1"))
            tm("select-window", "-t", "demo:0")
            record("select_a", drain())
            tm("select-window", "-t", "demo:1")
            record("select_b", drain())
            record("current_window_bell", bell("demo:1"))
            os.write(master, b"\x1b[O")
            record("focus_out", drain())
            record("unfocused_current_window_bell", bell("demo:1"))
            os.write(master, b"\x1b[I")
            record("focus_in", drain())
            tm("select-window", "-t", "demo:2")
            tm("set-window-option", "-t", "demo:0", "monitor-activity", "on")
            tm("send-keys", "-t", "demo:0", "printf 'ordinary output\\n'", "Enter")
            record("ordinary_output_with_activity_monitor", drain())
            tm("select-window", "-t", "demo:0")
            record("select_activity_window", drain())
            results["pass"] = all([
                results["stages"][1]["alerts"] == "0!",
                results["stages"][2]["alerts"] == "0!,1!",
                results["stages"][3]["alerts"] == "1!",
                results["stages"][4]["alerts"] == "",
                results["stages"][1]["titles_emitted"] == ["[!] demo:observer"],
                results["stages"][3]["titles_emitted"] == ["[!] demo:agent-a"],
                results["stages"][4]["titles_emitted"] == ["demo:agent-b"],
            ])
            print(json.dumps(results, ensure_ascii=False, indent=2))
        finally:
            subprocess.run(base + ["kill-server"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if master is not None:
                os.close(master)
            if child:
                os.waitpid(child, 0)


if __name__ == "__main__":
    main()

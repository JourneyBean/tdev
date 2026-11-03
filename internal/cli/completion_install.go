package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/shellpath"
)

type completionInstallation struct {
	Shell string             `json:"shell"`
	Files []shellpath.Change `json:"files"`
}

func detectedCompletionShell() string {
	shell := filepath.Base(os.Getenv("SHELL"))
	if shell == "bash" || shell == "zsh" {
		return shell
	}
	return "all"
}

func validCompletionMode(mode string) bool {
	return mode == "auto" || mode == "none" || mode == "bash" || mode == "zsh" || mode == "all"
}

func (a *app) completionInstallCommand(parent *cobra.Command) {
	c := &cobra.Command{Use: "install", Short: "Configure bash/zsh completion in shell startup files", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		shell, _ := c.Flags().GetString("shell")
		if shell == "auto" {
			shell = detectedCompletionShell()
		}
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		result, err := a.installCompletion(c.Context(), binary, shell)
		if err != nil {
			return err
		}
		if a.json {
			return a.result(result, nil)
		}
		fmt.Fprintln(a.out, "Completion configured. Open a new terminal to enable it.")
		for _, file := range result.Files {
			fmt.Fprintln(a.out, "Configured:", file.File)
			if file.Backup != "" {
				fmt.Fprintln(a.out, "Backup:", file.Backup)
			}
		}
		return nil
	}}
	c.Flags().String("shell", "auto", "shell: auto, bash, zsh or all")
	parent.AddCommand(c)
}

func completionSnippet(binary, shell string) string {
	quoted := shellpath.Quote(binary, shell)
	guard := "BASH_VERSION"
	if shell == "zsh" {
		guard = "ZSH_VERSION"
	}
	init := ""
	if shell == "zsh" {
		init = "    if ! type compdef >/dev/null 2>&1; then autoload -Uz compinit && compinit -i; fi\n"
	}
	alias := "      complete -o default -o nospace -F __start_tdev "
	if shell == "zsh" {
		alias = "      compdef _tdev "
	}
	alias += shellpath.Quote(filepath.Base(binary), shell) + " " + quoted + "\n"
	// eval keeps shared .profile files valid POSIX sh; only the matching
	// interactive shell evaluates its own generated completion script.
	return "case $- in\n  *i*)\n    if [ -n \"${" + guard + "-}\" ] && [ -x " + quoted + " ]; then\n" + init +
		"      eval \"$(" + quoted + " completion " + shell + ")\"\n" + alias + "    fi\n    ;;\nesac"
}

func (a *app) installCompletion(ctx context.Context, binary, shell string) (completionInstallation, error) {
	result := completionInstallation{Shell: shell}
	if shell != "bash" && shell != "zsh" && shell != "all" {
		return result, config.Invalid("completion shell must be bash, zsh or all")
	}
	var err error
	binary, err = filepath.Abs(binary)
	if err != nil {
		return result, err
	}
	if !config.SafeText(binary) {
		return result, config.Invalid("invalid completion executable path")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return result, err
	}
	shells := []string{shell}
	if shell == "all" {
		shells = []string{"bash", "zsh"}
	}
	type pending struct{ path, snippet string }
	var updates []pending
	for _, name := range shells {
		targets, err := shellpath.Targets(home, name, os.Getenv("ZDOTDIR"), os.Getenv("XDG_CONFIG_HOME"))
		if err != nil {
			return result, err
		}
		for _, target := range targets {
			updates = append(updates, pending{target, completionSnippet(binary, name)})
		}
	}
	// Share the existing PATH configuration lock for changes to the same files.
	unlock, err := a.store.Lock(ctx, "shell-path", "configure-path")
	if err != nil {
		return result, err
	}
	defer unlock()
	for _, update := range updates {
		change, err := shellpath.WriteBlock(update.path, update.snippet, "completion")
		if err != nil {
			return result, protocol.Fail("PARTIAL_SUCCESS", 9, "completion configuration at %s failed: %v; previously configured files: %v", update.path, err, result.Files)
		}
		result.Files = append(result.Files, change)
	}
	return result, nil
}

func (a *app) configureCompletionAfterInstall(ctx context.Context, mode, binary string) (*completionInstallation, error) {
	if mode == "none" {
		return nil, nil
	}
	shell := mode
	if shell == "auto" {
		if a.json || !terminalAvailable(a.in) {
			if !a.json {
				fmt.Fprintln(a.errOut, "Configure completion later: tdev completion install --shell auto")
			}
			return nil, nil
		}
		shell = detectedCompletionShell()
		fmt.Fprintf(a.errOut, "Installed %s.\n", binary)
		accepted, err := a.wizardYes(ctx, "Configure "+strings.ReplaceAll(shell, "all", "bash and zsh")+" command completion?", true)
		if err != nil {
			return nil, protocol.Fail("PARTIAL_SUCCESS", 9, "binary installed; completion configuration cancelled")
		}
		if !accepted {
			return nil, nil
		}
	}
	result, err := a.installCompletion(ctx, binary, shell)
	return &result, err
}

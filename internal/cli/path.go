package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/shellpath"
)

func (a *app) pathCommand() {
	c := &cobra.Command{Use: "path", Short: "Show or configure the user shell PATH", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		directory, _ := c.Flags().GetString("directory")
		if directory == "" {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			directory = filepath.Dir(exe)
		}
		directory, err := filepath.Abs(directory)
		if err != nil {
			return err
		}
		if strings.ContainsAny(directory, "\r\n\x00:") {
			return config.Invalid("PATH directory contains an unsupported separator/control character")
		}
		shell, _ := c.Flags().GetString("shell")
		if shell == "auto" {
			shell = filepath.Base(os.Getenv("SHELL"))
			if shell == "." {
				shell = "sh"
			}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		targets, err := shellpath.Targets(home, shell, os.Getenv("ZDOTDIR"), os.Getenv("XDG_CONFIG_HOME"))
		if err != nil {
			return config.Invalid("%s", err)
		}
		snippet := shellpath.Snippet(directory, shell)
		write, _ := c.Flags().GetBool("write")
		var changes []shellpath.Change
		if write {
			unlock, err := a.store.Lock(c.Context(), "shell-path", "configure-path")
			if err != nil {
				return err
			}
			defer unlock()
			for _, target := range targets {
				change, err := shellpath.Write(target, snippet)
				if err != nil {
					return fmt.Errorf("PATH configuration at %s: %w", target, err)
				}
				changes = append(changes, change)
			}
		}
		if a.json {
			return a.result(map[string]any{"directory": directory, "shell": shell, "snippet": snippet, "files": changes}, nil)
		}
		if write {
			fmt.Fprintln(a.out, "PATH configured. Open a new terminal, or run the following in your current shell:")
		}
		fmt.Fprintln(a.out, snippet)
		for _, change := range changes {
			if change.Backup != "" {
				fmt.Fprintf(a.out, "Backup: %s\n", change.Backup)
			}
		}
		return nil
	}}
	c.Flags().Bool("write", false, "persist an idempotent PATH block in shell startup files")
	c.Flags().String("shell", "auto", "shell: auto, bash, zsh, fish or sh")
	c.Flags().String("directory", "", "directory to add (defaults to this executable's directory)")
	a.root.AddCommand(c)
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/aiskill"
	"tdev/internal/config"
	"tdev/internal/protocol"
)

func validSkillsMode(mode string) bool {
	if mode == "" || mode == "auto" || mode == "none" || mode == "detected" {
		return true
	}
	for _, agent := range strings.Split(mode, ",") {
		switch agent {
		case "codex", "codex-legacy", "cc", "pi", "custom":
		default:
			return false
		}
	}
	return true
}

// Approval is separate from -y, which covers existing workspace/binary operations.
func (a *app) chooseSkills(ctx context.Context, targets []aiskill.Target) (string, error) {
	mode := a.skillsMode
	if !validSkillsMode(mode) {
		return "", config.Invalid("skills must be auto, none, detected or comma-separated agent names")
	}
	if mode == "none" {
		return "none", nil
	}
	selected := []string{}
	for _, target := range targets {
		accepted := mode == "detected" || strings.Contains(","+mode+",", ","+target.Agent+",")
		if mode == "auto" || mode == "" {
			if !a.json && terminalAvailable(a.in) {
				var err error
				accepted, err = a.wizardYes(ctx, fmt.Sprintf("Install or update tdev skill for %s at %s?", target.Agent, target.Directory), false)
				if err != nil {
					return "", err
				}
			}
		}
		if accepted {
			selected = append(selected, target.Agent)
		}
	}
	if len(selected) == 0 {
		return "none", nil
	}
	return strings.Join(selected, ","), nil
}

func (a *app) installSkills(ctx context.Context, agent, directory string) (any, error) {
	targets, err := aiskill.Targets(agent, directory)
	if err != nil {
		return nil, err
	}
	targets = aiskill.Detect(targets)
	mode, err := a.chooseSkills(ctx, targets)
	if err != nil {
		return nil, err
	}
	results := []aiskill.Result{}
	approved := []aiskill.Target{}
	for _, target := range targets {
		if strings.Contains(","+mode+",", ","+target.Agent+",") {
			approved = append(approved, target)
		} else {
			results = append(results, aiskill.Result{Target: target, Status: "skipped"})
		}
	}
	installed, err := aiskill.Install(ctx, approved)
	results = append(results, installed...)
	if err != nil {
		return results, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "skill installation needs attention: " + err.Error(), Details: results}
	}
	return results, nil
}

func (a *app) skillCommands() { a.skillCommandsOn(a.root, "skill") }
func (a *app) skillCommandsOn(parent *cobra.Command, name string) {
	skill := group(parent, name, "Inspect or install the bundled tdev AI skill")
	a.command(skill, "list", "Detect installed agents without writing directories", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		targets, err := aiskill.Targets("all", "")
		if err != nil {
			return nil, err
		}
		return aiskill.Detect(targets), nil
	})
	a.command(skill, "show", "Read the bundled skill", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		return map[string]string{"name": "tdev", "content": string(aiskill.Content())}, nil
	})
	c := a.command(skill, "install", "Confirm and install skills for detected agents", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		agent, _ := c.Flags().GetString("agent")
		directory, _ := c.Flags().GetString("directory")
		return a.installSkills(ctx, agent, directory)
	})
	c.Flags().String("agent", "all", "target agent: all, codex, codex-legacy, cc or pi")
	c.Flags().String("directory", "", "absolute skills root for another compatible agent")
}

// Probe the verified target; only the foreground client reads confirmations.
func (a *app) remoteSkills(ctx context.Context, t target) (string, error) {
	if !validSkillsMode(a.skillsMode) {
		return "", config.Invalid("invalid skills mode")
	}
	if a.skillsMode == "none" || ((a.skillsMode == "auto" || a.skillsMode == "") && (a.json || !terminalAvailable(a.in))) {
		return "none", nil
	}
	data, err := a.call(ctx, t, nil, "binary", "skills", "list")
	if err != nil {
		return "", err
	}
	var targets []aiskill.Target
	if err = json.Unmarshal(data, &targets); err != nil {
		return "", err
	}
	return a.chooseSkills(ctx, targets)
}

func skillModeArgs(args []string, mode string) []string {
	// A JSON target's default auto mode skips writes. Omitting the flag for a
	// skipped install preserves compatibility with historical target binaries.
	if mode != "none" && mode != "" {
		return append(args, "--skills", mode)
	}
	return args
}

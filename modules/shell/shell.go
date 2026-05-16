// Package shell implements the ansible.builtin.shell module.
//
// This module executes shell commands on target hosts. It is equivalent to the
// Ansible `shell` module and accepts the same core parameters.
//
// Module Name: ansible.builtin.shell
// Aliases:     shell
//
// # Parameters
//
//   - _raw_params (string, required): The shell command to run. This is what you
//     write as a free-form string in a playbook task.
//   - chdir (string, optional): Change into this directory before running the command.
//   - executable (string, optional): Override the shell used to run the command
//     (default: /bin/sh).
//   - creates (string, optional): If this file exists, the task is skipped.
//   - removes (string, optional): If this file does NOT exist, the task is skipped.
//   - stdin (string, optional): Feed this string to the command's standard input.
//
// # Registration
//
// This module self-registers via init(). Import it with a blank identifier to
// include it in your binary:
//
//	import _ "github.com/sagan/gosible/modules/shell"
package shell

import (
	"fmt"
	"strings"

	"github.com/sagan/gosible/internal/modules"
)

func init() {
	modules.Register(&shellModule{})
}

type shellModule struct{}

func (m *shellModule) Name() string    { return "ansible.builtin.shell" }
func (m *shellModule) Aliases() []string { return []string{"shell"} }

// Run executes the shell command on the target host.
func (m *shellModule) Run(ctx *modules.ModuleContext) (*modules.Result, error) {
	args := ctx.Args

	// Resolve the command to run.
	cmd, err := resolveCommand(args)
	if err != nil {
		return &modules.Result{Host: ctx.Host, Failed: true, Msg: err.Error()}, nil
	}

	// Handle "creates" skip condition.
	if creates, ok := modules.StringArg(args, "creates"); ok && creates != "" {
		exists, err := modules.FileExists(ctx, creates)
		if err != nil {
			return &modules.Result{Host: ctx.Host, Failed: true, Msg: fmt.Sprintf("creates check failed: %v", err)}, nil
		}
		if exists {
			return &modules.Result{
				Host:    ctx.Host,
				Changed: false,
				Msg:     fmt.Sprintf("skipped, since %s exists", creates),
			}, nil
		}
	}

	// Handle "removes" skip condition.
	if removes, ok := modules.StringArg(args, "removes"); ok && removes != "" {
		exists, err := modules.FileExists(ctx, removes)
		if err != nil {
			return &modules.Result{Host: ctx.Host, Failed: true, Msg: fmt.Sprintf("removes check failed: %v", err)}, nil
		}
		if !exists {
			return &modules.Result{
				Host:    ctx.Host,
				Changed: false,
				Msg:     fmt.Sprintf("skipped, since %s does not exist", removes),
			}, nil
		}
	}

	// Build the full shell invocation.
	fullCmd := buildFullCommand(cmd, args, ctx.Become, ctx.BecomeUser)

	stdout, stderr, rc, err := ctx.Connection.RunCommand(fullCmd)
	if err != nil {
		return &modules.Result{
			Host:   ctx.Host,
			Failed: true,
			Msg:    fmt.Sprintf("connection error: %v", err),
		}, nil
	}

	failed := rc != 0
	msg := ""
	if failed {
		msg = fmt.Sprintf("non-zero return code %d", rc)
	} else {
		msg = "Command executed successfully"
	}

	return &modules.Result{
		Host:    ctx.Host,
		Changed: true, // shell module always reports changed (we cannot know otherwise)
		Failed:  failed,
		Msg:     msg,
		Stdout:  stdout,
		Stderr:  stderr,
		RC:      rc,
	}, nil
}

// resolveCommand extracts the command string from args.
// Supports both free-form (_raw_params) and explicit "cmd" parameter.
func resolveCommand(args modules.Args) (string, error) {
	// Free-form invocation (e.g. "shell: echo hello")
	if raw, ok := args["_raw_params"]; ok {
		cmd := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if cmd != "" {
			return cmd, nil
		}
	}
	// Explicit "cmd" parameter (e.g. shell: { cmd: "echo hello" })
	if cmd, ok := args["cmd"]; ok {
		s := strings.TrimSpace(fmt.Sprintf("%v", cmd))
		if s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("ansible.builtin.shell: 'cmd' or free-form command is required")
}

// buildFullCommand wraps the user command with optional chdir, executable,
// become (sudo), and stdin.
func buildFullCommand(cmd string, args modules.Args, become bool, becomeUser string) string {
	executable := "/bin/sh"
	if exe, ok := modules.StringArg(args, "executable"); ok && exe != "" {
		executable = exe
	}

	// Wrap in privilege escalation if requested.
	if become && becomeUser != "" {
		cmd = fmt.Sprintf("sudo -u %s %s -c %s", becomeUser, executable, modules.ShellQuote(cmd))
	} else {
		cmd = fmt.Sprintf("%s -c %s", executable, modules.ShellQuote(cmd))
	}

	// Change directory prefix.
	if chdir, ok := modules.StringArg(args, "chdir"); ok && chdir != "" {
		cmd = fmt.Sprintf("cd %s && %s", modules.ShellQuote(chdir), cmd)
	}

	return cmd
}

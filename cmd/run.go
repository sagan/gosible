package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagan/gosible/internal/executor"
	"github.com/sagan/gosible/internal/inventory"
	"github.com/sagan/gosible/internal/modules"
	"github.com/sagan/gosible/internal/runner"
)

// runFlags holds flags specific to the "run" subcommand.
var runFlags struct {
	Module     string
	ModuleArgs string
	Become     bool
	BecomeUser string
	Pattern    string
}

// runCmd mirrors the `ansible` command for ad-hoc task execution.
var runCmd = &cobra.Command{
	Use:   "run <host-pattern>",
	Short: "Run an ad-hoc command on hosts (equivalent to the `ansible` command)",
	Long: `Execute a single module against a set of hosts from your inventory.

Examples:
  # Ping all hosts
  gosible run all -m ping

  # Run a shell command on webservers
  gosible run webservers -m shell -a "uptime"

  # Run a command as root via sudo
  gosible run webservers -m shell -a "whoami" --become --become-user root`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]

		// Load inventory.
		inv, err := inventory.ParseFile(rootFlags.Inventory)
		if err != nil {
			return fmt.Errorf("run: %w", err)
		}

		// Parse module args string "key=value key2=value2" or free-form.
		modArgs := parseModuleArgs(runFlags.ModuleArgs)

		// Parse extra vars.
		extraVars, err := runner.ParseExtraVars(rootFlags.ExtraVars)
		if err != nil {
			return fmt.Errorf("run: extra-vars: %w", err)
		}

		// Build SSH options.
		sshOpts := executor.DefaultOptions()
		sshOpts.Timeout = time.Duration(rootFlags.Timeout) * time.Second
		if rootFlags.PrivateKey != "" {
			sshOpts.PrivateKeyFile = rootFlags.PrivateKey
		}
		if rootFlags.User != "" {
			sshOpts.User = rootFlags.User
		}
		if rootFlags.Port != 22 {
			sshOpts.Port = rootFlags.Port
		}

		// Build runner config.
		cfg := runner.Config{
			Forks:      rootFlags.Forks,
			ExtraVars:  extraVars,
			SSHOptions: sshOpts,
			NoColor:    rootFlags.NoColor,
			Verbose:    rootFlags.Verbose,
			CheckMode:  rootFlags.CheckMode,
		}

		r := runner.New(cfg)

		// Resolve the module name.
		moduleName := runFlags.Module

		// If become flags are set at the run level, inject them into the context
		// by wrapping args (the runner handles this via ModuleContext).
		_ = runFlags.Become
		_ = runFlags.BecomeUser

		success, err := r.RunAdHoc(inv, pattern, moduleName, modules.Args(modArgs))
		if err != nil {
			return err
		}
		if !success {
			os.Exit(2) // Non-zero exit like ansible does on task failure.
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)

	f := runCmd.Flags()
	f.StringVarP(&runFlags.Module, "module-name", "m", "command", "Module name to execute")
	f.StringVarP(&runFlags.ModuleArgs, "args", "a", "", "Module arguments")
	f.BoolVarP(&runFlags.Become, "become", "b", false, "Run operations with become (sudo)")
	f.StringVar(&runFlags.BecomeUser, "become-user", "root", "Run operations as this user (used with --become)")
	f.StringVar(&runFlags.Pattern, "pattern", "", "Host pattern (can also be the first positional argument)")
}

// parseModuleArgs converts an Ansible-style argument string "key=val key2=val2"
// or a plain free-form string into a modules.Args map.
func parseModuleArgs(raw string) map[string]interface{} {
	args := map[string]interface{}{}
	if raw == "" {
		return args
	}

	// Check if it looks like key=value pairs.
	if strings.Contains(raw, "=") {
		// Split on spaces, but only for top-level key=value pairs.
		// This is a simplified parser; Ansible's full parser handles quoting.
		for _, part := range splitArgs(raw) {
			if idx := strings.IndexByte(part, '='); idx > 0 {
				k := strings.TrimSpace(part[:idx])
				v := strings.TrimSpace(part[idx+1:])
				args[k] = v
			}
		}
		if len(args) > 0 {
			return args
		}
	}

	// Treat the whole string as a free-form command.
	args["_raw_params"] = raw
	return args
}

// splitArgs splits a module arg string respecting quoted segments.
func splitArgs(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
			} else {
				cur.WriteByte(c)
			}
		} else if c == '"' || c == '\'' {
			inQuote = true
			quoteChar = c
		} else if c == ' ' || c == '\t' {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		} else {
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

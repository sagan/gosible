// Package cmd provides the Cobra CLI for gosible.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// rootFlags holds global flags shared by all subcommands.
var rootFlags struct {
	Inventory  string
	Forks      int
	ExtraVars  []string
	Verbose    bool
	NoColor    bool
	CheckMode  bool
	PrivateKey string
	User       string
	Port       int
	AskPass    bool
	Timeout    int
}

// rootCmd is the top-level gosible command.
var rootCmd = &cobra.Command{
	Use:   "gosible",
	Short: "A lightweight Ansible-compatible automation tool written in Go",
	Long: `gosible is a lightweight Ansible replacement written in Go.
It uses the same playbook format as Ansible but leverages Go text/template
for variable substitution instead of Jinja2.

Subcommands:
  run   – Run an ad-hoc command on hosts (ansible equivalent)
  play  – Execute a playbook (ansible-playbook equivalent)`,
	Version: "0.1.0",
}

// Execute runs the root command. Call this from main().
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()

	pf.StringVarP(&rootFlags.Inventory, "inventory", "i", "inventory", "Path to inventory file or directory")
	pf.IntVarP(&rootFlags.Forks, "forks", "f", 5, "Number of parallel processes to use")
	pf.StringArrayVarP(&rootFlags.ExtraVars, "extra-vars", "e", nil, "Set additional variables as key=value or @file.json")
	pf.BoolVarP(&rootFlags.Verbose, "verbose", "v", false, "Verbose output")
	pf.BoolVar(&rootFlags.NoColor, "no-color", false, "Disable ANSI color output")
	pf.BoolVarP(&rootFlags.CheckMode, "check", "C", false, "Dry run: do not make any changes")
	pf.StringVar(&rootFlags.PrivateKey, "private-key", "", "Use this file to authenticate the connection")
	pf.StringVarP(&rootFlags.User, "user", "u", "", "Connect as this user (overrides ansible_user)")
	pf.IntVarP(&rootFlags.Port, "port", "p", 22, "Connect to this port")
	pf.BoolVarP(&rootFlags.AskPass, "ask-pass", "k", false, "Ask for connection password")
	pf.IntVar(&rootFlags.Timeout, "timeout", 10, "Override connection timeout (seconds)")
}

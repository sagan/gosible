package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagan/gosible/internal/executor"
	"github.com/sagan/gosible/internal/inventory"
	"github.com/sagan/gosible/internal/playbook"
	"github.com/sagan/gosible/internal/runner"
)

// playFlags holds flags specific to the "play" subcommand.
var playFlags struct {
	Become     bool
	BecomeUser string
	Tags       []string
	SkipTags   []string
	StartAt    string
	ListTasks  bool
	ListHosts  bool
}

// playCmd mirrors the `ansible-playbook` command.
var playCmd = &cobra.Command{
	Use:   "play <playbook.yml> [playbook2.yml ...]",
	Short: "Execute an Ansible-compatible playbook (equivalent to `ansible-playbook`)",
	Long: `Run one or more YAML playbooks against your inventory.

Playbooks use the standard Ansible format with Go text/template for variable
substitution ({{ .variable_name }}) instead of Jinja2.

Examples:
  # Run a playbook
  gosible play site.yml

  # Run with a specific inventory and extra variables
  gosible play site.yml -i production -e version=1.2.3

  # Dry-run (check mode)
  gosible play site.yml --check

  # Run only tasks with specific tags
  gosible play site.yml --tags deploy,config`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse extra vars.
		extraVars, err := runner.ParseExtraVars(rootFlags.ExtraVars)
		if err != nil {
			return fmt.Errorf("play: extra-vars: %w", err)
		}

		// Load inventory.
		inv, err := inventory.Parse(rootFlags.Inventory)
		if err != nil {
			return fmt.Errorf("play: %w", err)
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

		// Execute each playbook file in order.
		overallSuccess := true
		for _, pbFile := range args {
			pb, err := playbook.ParseFile(pbFile, extraVars)
			if err != nil {
				return fmt.Errorf("play: parse %q: %w", pbFile, err)
			}

			// Handle --list-hosts
			if playFlags.ListHosts {
				if err := listHosts(pb, inv); err != nil {
					return err
				}
				continue
			}

			// Handle --list-tasks
			if playFlags.ListTasks {
				listTasks(pb)
				continue
			}

			success, err := r.RunPlaybook(pb, inv)
			if err != nil {
				return fmt.Errorf("play: run %q: %w", pbFile, err)
			}
			if !success {
				overallSuccess = false
			}
		}

		if !overallSuccess {
			os.Exit(2)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(playCmd)

	f := playCmd.Flags()
	f.BoolVarP(&playFlags.Become, "become", "b", false, "Run operations with become (sudo)")
	f.StringVar(&playFlags.BecomeUser, "become-user", "root", "Run operations as this user (used with --become)")
	f.StringArrayVar(&playFlags.Tags, "tags", nil, "Only run plays and tasks tagged with these values")
	f.StringArrayVar(&playFlags.SkipTags, "skip-tags", nil, "Skip plays and tasks with these tags")
	f.StringVar(&playFlags.StartAt, "start-at-task", "", "Start the playbook at the task matching this name")
	f.BoolVar(&playFlags.ListTasks, "list-tasks", false, "List all tasks that would be executed")
	f.BoolVar(&playFlags.ListHosts, "list-hosts", false, "Outputs a list of matching hosts")
}

// listHosts prints the hosts that would be targeted by each play.
func listHosts(pb *playbook.Playbook, inv *inventory.Inventory) error {
	fmt.Printf("Playbook: %s\n\n", pb.Path)
	for i, play := range pb.Plays {
		fmt.Printf("  play #%d (%s): %s\n", i+1, play.Hosts, play.Name)
		hosts, err := inv.MatchPattern(play.Hosts)
		if err != nil {
			return err
		}
		fmt.Printf("    pattern: %s\n    hosts (%d):\n", play.Hosts, len(hosts))
		for _, h := range hosts {
			fmt.Printf("      %s\n", h.Name)
		}
		fmt.Println()
	}
	return nil
}

// listTasks prints all tasks in all plays.
func listTasks(pb *playbook.Playbook) {
	fmt.Printf("Playbook: %s\n\n", pb.Path)
	for i, play := range pb.Plays {
		fmt.Printf("  play #%d (%s): %s\n", i+1, play.Hosts, play.Name)
		for j, task := range play.Tasks {
			name := task.Name
			if name == "" {
				name = task.ModuleName
			}
			fmt.Printf("    task #%d : %s (%s)\n", j+1, name, task.ModuleName)
		}
		fmt.Println()
	}
}

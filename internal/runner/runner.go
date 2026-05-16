// Package runner orchestrates playbook and ad-hoc execution.
// It ties together inventory, playbook parsing, connection management,
// module dispatch, and output formatting.
package runner

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sagan/gosible/internal/executor"
	"github.com/sagan/gosible/internal/inventory"
	"github.com/sagan/gosible/internal/modules"
	"github.com/sagan/gosible/internal/output"
	"github.com/sagan/gosible/internal/playbook"
)

// Config holds the runtime configuration for a runner.
type Config struct {
	// Forks is the number of hosts to execute against in parallel.
	Forks int
	// ExtraVars are additional variables passed via CLI (-e flag).
	ExtraVars map[string]interface{}
	// SSHOptions controls SSH connection behaviour.
	SSHOptions executor.Options
	// NoColor disables ANSI color output.
	NoColor bool
	// Verbose enables verbose output (shows stdout/stderr even on success).
	Verbose bool
	// CheckMode performs a dry-run without making changes.
	CheckMode bool
}

// DefaultConfig returns sensible runner defaults.
func DefaultConfig() Config {
	return Config{
		Forks:      5,
		ExtraVars:  map[string]interface{}{},
		SSHOptions: executor.DefaultOptions(),
	}
}

// Runner executes playbooks or ad-hoc tasks against an inventory.
type Runner struct {
	cfg     Config
	printer *output.Printer
}

// New creates a new Runner with the given configuration.
func New(cfg Config) *Runner {
	return &Runner{
		cfg:     cfg,
		printer: output.New(cfg.NoColor),
	}
}

// RunPlaybook executes all plays in the given playbook against the inventory.
// Returns true if all tasks succeeded, false if any failed.
func (r *Runner) RunPlaybook(pb *playbook.Playbook, inv *inventory.Inventory) (bool, error) {
	overallSuccess := true
	globalStats := map[string]*output.HostStats{}

	for i := range pb.Plays {
		play := &pb.Plays[i]
		success, stats, err := r.runPlay(play, inv)
		if err != nil {
			return false, err
		}
		if !success {
			overallSuccess = false
		}
		for host, s := range stats {
			if _, exists := globalStats[host]; !exists {
				globalStats[host] = &output.HostStats{}
			}
			globalStats[host].OK += s.OK
			globalStats[host].Changed += s.Changed
			globalStats[host].Failed += s.Failed
			globalStats[host].Unreachable += s.Unreachable
		}
	}

	r.printer.Recap(globalStats)
	return overallSuccess, nil
}

// RunAdHoc runs a single module invocation against matched hosts.
func (r *Runner) RunAdHoc(inv *inventory.Inventory, pattern, moduleName string, args modules.Args) (bool, error) {
	hosts, err := inv.MatchPattern(pattern)
	if err != nil {
		return false, err
	}

	mod, err := modules.Lookup(moduleName)
	if err != nil {
		return false, err
	}

	r.printer.TaskBanner(moduleName)

	stats := map[string]*output.HostStats{}
	for _, h := range hosts {
		stats[h.Name] = &output.HostStats{}
	}

	results := r.executeOnHosts(hosts, mod, args, map[string]interface{}{}, false, "root")
	success := true
	for _, res := range results {
		r.printer.PrintResult(res)
		s := stats[res.Host]
		switch {
		case res.Failed:
			s.Failed++
			success = false
		case res.Changed:
			s.Changed++
		default:
			s.OK++
		}
	}

	r.printer.Recap(stats)
	return success, nil
}

// runPlay executes a single play and returns success status and per-host stats.
func (r *Runner) runPlay(play *playbook.Play, inv *inventory.Inventory) (bool, map[string]*output.HostStats, error) {
	hosts, err := inv.MatchPattern(play.Hosts)
	if err != nil {
		return false, nil, fmt.Errorf("runner: play %q: %w", play.Name, err)
	}

	r.printer.PlayBanner(play.Name)

	stats := map[string]*output.HostStats{}
	for _, h := range hosts {
		stats[h.Name] = &output.HostStats{}
	}

	// Registered variables, keyed by host then variable name.
	registered := map[string]map[string]interface{}{}
	for _, h := range hosts {
		registered[h.Name] = map[string]interface{}{}
	}

	// Pending handler notifications (deduped).
	pendingHandlers := map[string]bool{}

	success := true

	for i := range play.Tasks {
		task := &play.Tasks[i]

		mod, err := modules.Lookup(task.ModuleName)
		if err != nil {
			return false, stats, fmt.Errorf("runner: play %q task %q: %w", play.Name, task.Name, err)
		}

		// Determine per-task become settings (task overrides play).
		become := play.Become
		if task.Become != nil {
			become = *task.Become
		}
		becomeUser := play.BecomeUser
		if task.BecomeUser != "" {
			becomeUser = task.BecomeUser
		}
		if becomeUser == "" {
			becomeUser = "root"
		}

		// Execute on all hosts in this task (respecting forks).
		taskHosts := filterAliveHosts(hosts, stats)

		// Only print the banner when at least one host is still active.
		if len(taskHosts) > 0 {
			r.printer.TaskBanner(taskLabel(task))
		}

		// Merge play vars + extra vars (registered vars for each host handled inside).
		playVars := mergeVars(play.Vars, r.cfg.ExtraVars)

		// Build module args (convert map[string]interface{} to modules.Args).
		args := modules.Args(task.ModuleArgs)

		results := r.executeOnHosts(taskHosts, mod, args, playVars, become, becomeUser)

		for _, res := range results {
			r.printer.PrintResult(res)
			s := stats[res.Host]

			if res.Failed && !task.IgnoreErrors {
				s.Failed++
				success = false
			} else if res.Changed {
				s.Changed++
				// Trigger handler notifications.
				for _, h := range task.Notify {
					pendingHandlers[h] = true
				}
			} else {
				// Covers: ok (unchanged/skipped) AND failed-but-ignored tasks.
				// Ansible counts ignored failures as ok in the recap.
				s.OK++
			}

			// Store registered variable.
			if task.Register != "" {
				registered[res.Host][task.Register] = resultToMap(res)
			}
		}
	}

	// Run triggered handlers.
	for i := range play.Handlers {
		handler := &play.Handlers[i]
		if !pendingHandlers[handler.Name] {
			continue
		}
		mod, err := modules.Lookup(handler.ModuleName)
		if err != nil {
			return false, stats, fmt.Errorf("runner: handler %q: %w", handler.Name, err)
		}
		r.printer.TaskBanner("HANDLER: " + handler.Name)
		args := modules.Args(handler.ModuleArgs)
		playVars := mergeVars(play.Vars, r.cfg.ExtraVars)
		results := r.executeOnHosts(filterAliveHosts(hosts, stats), mod, args, playVars, play.Become, firstStr(play.BecomeUser, "root"))
		for _, res := range results {
			r.printer.PrintResult(res)
		}
	}

	return success, stats, nil
}

// executeOnHosts runs a module on a slice of hosts in parallel (up to r.cfg.Forks).
func (r *Runner) executeOnHosts(hosts []*inventory.Host, mod modules.Module, args modules.Args, vars map[string]interface{}, become bool, becomeUser string) []*modules.Result {
	type indexedResult struct {
		idx int
		res *modules.Result
	}

	results := make([]*modules.Result, len(hosts))
	ch := make(chan indexedResult, len(hosts))
	sem := make(chan struct{}, r.cfg.Forks)

	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(idx int, h *inventory.Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := r.executeOnHost(h, mod, args, vars, become, becomeUser)
			ch <- indexedResult{idx: idx, res: res}
		}(i, host)
	}

	wg.Wait()
	close(ch)

	for ir := range ch {
		results[ir.idx] = ir.res
	}
	return results
}

// executeOnHost runs a module on a single host, establishing a connection.
func (r *Runner) executeOnHost(host *inventory.Host, mod modules.Module, args modules.Args, vars map[string]interface{}, become bool, becomeUser string) *modules.Result {
	// localhost is handled via local execution.
	var conn modules.Connection
	var connErr error

	if isLocal(host) {
		conn = &executor.LocalConnection{}
	} else {
		conn, connErr = executor.Dial(host, r.cfg.SSHOptions)
	}

	if connErr != nil {
		return &modules.Result{
			Host:   host.Name,
			Failed: true,
			Msg:    fmt.Sprintf("unreachable: %v", connErr),
		}
	}
	if conn != nil {
		defer conn.Close()
	}

	ctx := executor.NewModuleContext(host, conn, args, vars, become, becomeUser)
	result, err := mod.Run(ctx)
	if err != nil {
		return &modules.Result{
			Host:   host.Name,
			Failed: true,
			Msg:    err.Error(),
		}
	}
	if result.Host == "" {
		result.Host = host.Name
	}
	return result
}

// isLocal returns true if the host should be executed locally.
func isLocal(h *inventory.Host) bool {
	conn := h.GetVar("ansible_connection", "")
	if conn == "local" {
		return true
	}
	name := h.Name
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

// filterAliveHosts returns hosts that haven't failed yet (not marked as failed/unreachable).
func filterAliveHosts(hosts []*inventory.Host, stats map[string]*output.HostStats) []*inventory.Host {
	var alive []*inventory.Host
	for _, h := range hosts {
		if s, ok := stats[h.Name]; ok && (s.Failed > 0 || s.Unreachable > 0) {
			continue
		}
		alive = append(alive, h)
	}
	return alive
}

// mergeVars merges multiple var maps into one (later maps take priority).
func mergeVars(maps ...map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// resultToMap converts a Result to a map for variable registration.
func resultToMap(r *modules.Result) map[string]interface{} {
	m := map[string]interface{}{
		"changed": r.Changed,
		"failed":  r.Failed,
		"msg":     r.Msg,
		"stdout":  r.Stdout,
		"stderr":  r.Stderr,
		"rc":      r.RC,
	}
	for k, v := range r.Extra {
		m[k] = v
	}
	return m
}

// taskLabel returns a display label for a task.
func taskLabel(t *playbook.Task) string {
	if t.Name != "" {
		return t.Name
	}
	return t.ModuleName
}

// firstStr returns the first non-empty string.
func firstStr(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ParseExtraVars parses a slice of "key=value" or "@file.json" strings into a map.
func ParseExtraVars(raw []string) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	for _, s := range raw {
		if strings.Contains(s, "=") {
			k, v := splitOnce(s, '=')
			result[k] = v
		}
	}
	return result, nil
}

func splitOnce(s string, sep byte) (string, string) {
	idx := strings.IndexByte(s, sep)
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

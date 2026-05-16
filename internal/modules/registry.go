// Package modules provides the core module registry for gosible.
// Modules are registered at compile time via init() functions, making the
// system extensible without requiring runtime plugins.
package modules

import (
	"fmt"
	"sync"
)

// Result holds the outcome of a module execution on a single host.
type Result struct {
	// Host is the target host this result belongs to.
	Host string
	// Changed indicates whether the module altered the host state.
	Changed bool
	// Failed indicates whether the module execution failed.
	Failed bool
	// Msg is a human-readable summary of the result.
	Msg string
	// Stdout is the standard output captured from the remote command.
	Stdout string
	// Stderr is the standard error captured from the remote command.
	Stderr string
	// RC is the return code from the remote command.
	RC int
	// Extra holds any additional module-specific result data.
	Extra map[string]interface{}
}

// Args is a map of task arguments passed to a module.
type Args map[string]interface{}

// ModuleContext carries all the context a module needs to execute.
type ModuleContext struct {
	// Host is the target hostname or IP address.
	Host string
	// Connection is the transport used to communicate with the host.
	Connection Connection
	// Args are the task arguments for this module invocation.
	Args Args
	// Vars holds all available variables (host vars, group vars, playbook vars).
	Vars map[string]interface{}
	// BecomeUser is the user to escalate to (e.g. "root"), if any.
	BecomeUser string
	// Become indicates whether privilege escalation is enabled.
	Become bool
}

// Module is the interface every gosible module must implement.
// Register implementations using Register() in an init() function.
type Module interface {
	// Name returns the canonical FQCN (Fully Qualified Collection Name) of the module,
	// e.g. "ansible.builtin.shell".
	Name() string
	// Aliases returns a list of short-form names that should resolve to this module,
	// e.g. ["shell"].
	Aliases() []string
	// Run executes the module logic and returns a Result.
	Run(ctx *ModuleContext) (*Result, error)
}

// Connection is the interface through which modules communicate with remote hosts.
type Connection interface {
	// RunCommand executes a shell command on the remote host and returns stdout, stderr,
	// and the exit code.
	RunCommand(cmd string) (stdout, stderr string, rc int, err error)
	// Close tears down the connection.
	Close() error
}

// registry is the global module store, keyed by all known names/aliases.
var (
	registry   = map[string]Module{}
	registryMu sync.RWMutex
)

// Register adds a module to the global registry. It panics if a module with
// the same name or alias is already registered, ensuring conflicts are caught
// at startup. Call this from an init() function in your module package.
func Register(m Module) {
	registryMu.Lock()
	defer registryMu.Unlock()

	all := append([]string{m.Name()}, m.Aliases()...)
	for _, name := range all {
		if _, exists := registry[name]; exists {
			panic(fmt.Sprintf("gosible/modules: module %q already registered", name))
		}
		registry[name] = m
	}
}

// Lookup returns the module registered under name, or an error if not found.
func Lookup(name string) (Module, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	m, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown module: %q (is the module imported?)", name)
	}
	return m, nil
}

// List returns the names of all registered modules (canonical names only).
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	seen := map[string]bool{}
	var names []string
	for _, m := range registry {
		if !seen[m.Name()] {
			seen[m.Name()] = true
			names = append(names, m.Name())
		}
	}
	return names
}

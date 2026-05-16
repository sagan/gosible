// Package playbook parses Ansible-compatible YAML playbook files.
// Variable substitution uses Go text/template ({{ .var }}) instead of Jinja2.
package playbook

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Play represents a single Ansible play in a playbook.
type Play struct {
	// Name is the human-readable description of the play.
	Name string `yaml:"name"`
	// Hosts is the host pattern this play targets (e.g. "all", "webservers").
	Hosts string `yaml:"hosts"`
	// Become indicates whether to escalate privileges for all tasks in this play.
	Become bool `yaml:"become"`
	// BecomeUser is the user to become (default "root").
	BecomeUser string `yaml:"become_user"`
	// Gather facts controls whether host facts are collected before tasks run.
	GatherFacts *bool `yaml:"gather_facts"`
	// Vars holds play-level variables.
	Vars map[string]interface{} `yaml:"vars"`
	// Tasks is the ordered list of tasks to execute.
	Tasks []Task `yaml:"tasks"`
	// Handlers are tasks triggered by notify directives.
	Handlers []Task `yaml:"handlers"`
}

// Task represents a single Ansible task within a play.
type Task struct {
	// Name is the human-readable description of the task.
	Name string `yaml:"name"`
	// Become overrides the play-level become setting for this task.
	Become *bool `yaml:"become"`
	// BecomeUser overrides the play-level become_user for this task.
	BecomeUser string `yaml:"become_user"`
	// When is a conditional expression (Go template evaluating to "true"/"false").
	When string `yaml:"when"`
	// Register is the variable name to store the task result in.
	Register string `yaml:"register"`
	// Notify is a list of handler names to trigger on change.
	Notify []string `yaml:"notify"`
	// IgnoreErrors, if true, continues even if the task fails.
	IgnoreErrors bool `yaml:"ignore_errors"`
	// Loop is a list of items to iterate over.
	Loop []interface{} `yaml:"loop"`
	// With is an alias for loop (legacy Ansible syntax).
	With []interface{} `yaml:"with_items"`

	// ModuleName is the resolved module name (set during parsing).
	ModuleName string
	// ModuleArgs are the parsed module arguments.
	ModuleArgs map[string]interface{}

	// raw holds the raw YAML node for deferred module arg parsing.
	raw map[string]interface{}
}

// knownTaskKeys lists top-level YAML keys that are task metadata, not module names.
var knownTaskKeys = map[string]bool{
	"name": true, "become": true, "become_user": true, "when": true,
	"register": true, "notify": true, "ignore_errors": true, "loop": true,
	"with_items": true, "tags": true, "vars": true, "block": true,
	"rescue": true, "always": true, "delegate_to": true, "environment": true,
}

// UnmarshalYAML implements yaml.Unmarshaler so we can extract the module name
// and its arguments from the remaining keys after metadata is decoded.
func (t *Task) UnmarshalYAML(value *yaml.Node) error {
	// First decode into a raw map to capture all keys.
	var raw map[string]interface{}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	t.raw = raw

	// Decode metadata fields using the standard decoder.
	type taskAlias Task
	var alias taskAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}
	*t = Task(alias)
	t.raw = raw

	// Find the module name: it's the key that is NOT a known metadata key.
	for k, v := range raw {
		if knownTaskKeys[k] {
			continue
		}
		t.ModuleName = k
		// Module args can be a string (free-form) or a map.
		switch val := v.(type) {
		case string:
			t.ModuleArgs = map[string]interface{}{"_raw_params": val}
		case map[string]interface{}:
			t.ModuleArgs = val
		default:
			t.ModuleArgs = map[string]interface{}{"_raw_params": fmt.Sprintf("%v", val)}
		}
		break
	}

	return nil
}

// Playbook is the parsed representation of an Ansible playbook file.
type Playbook struct {
	Plays []Play
	// Path is the file path from which this playbook was loaded.
	Path string
}

// ParseFile reads and parses an Ansible playbook YAML file.
// Variable references using Go text/template syntax ({{ .varname }}) in the
// raw YAML are rendered before parsing so that dynamic values are resolved.
func ParseFile(path string, extraVars map[string]interface{}) (*Playbook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("playbook: read %q: %w", path, err)
	}
	return ParseBytes(data, path, extraVars)
}

// ParseBytes parses a playbook from raw YAML bytes.
func ParseBytes(data []byte, path string, extraVars map[string]interface{}) (*Playbook, error) {
	// Render Go templates in the YAML before parsing.
	rendered, err := renderTemplate(data, extraVars)
	if err != nil {
		return nil, fmt.Errorf("playbook: template render: %w", err)
	}

	var plays []Play
	if err := yaml.Unmarshal(rendered, &plays); err != nil {
		return nil, fmt.Errorf("playbook: parse YAML: %w", err)
	}

	pb := &Playbook{Plays: plays, Path: path}

	// Validate plays have required fields.
	for i, play := range pb.Plays {
		if play.Hosts == "" {
			return nil, fmt.Errorf("playbook: play[%d] %q: missing 'hosts' field", i, play.Name)
		}
		for j, task := range play.Tasks {
			if task.ModuleName == "" {
				return nil, fmt.Errorf("playbook: play[%d] task[%d] %q: no module found", i, j, task.Name)
			}
		}
	}

	return pb, nil
}

// renderTemplate executes Go text/template substitution on raw YAML bytes.
func renderTemplate(data []byte, vars map[string]interface{}) ([]byte, error) {
	if vars == nil {
		vars = map[string]interface{}{}
	}
	tmpl, err := template.New("playbook").Option("missingkey=zero").Parse(string(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

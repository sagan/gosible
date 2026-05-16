# Gosible

A lightweight Ansible-compatible automation tool written in Go.

- [Gosible](#gosible)
  - [Overview](#overview)
  - [Design Goals](#design-goals)
  - [Architecture](#architecture)
    - [Module System](#module-system)
  - [Installation](#installation)
  - [Usage](#usage)
    - [Ad-hoc commands (`run`)](#ad-hoc-commands-run)
    - [Playbooks (`play`)](#playbooks-play)
  - [Playbook Format](#playbook-format)
  - [Inventory Format](#inventory-format)
  - [Implemented Modules](#implemented-modules)
    - [ansible.builtin.shell Parameters](#ansiblebuiltinshell-parameters)
  - [Adding New Modules](#adding-new-modules)
  - [Testing](#testing)
  - [License](#license)

## Overview

gosible implements the core functions of [Ansible](https://docs.ansible.com/) using the same playbook YAML format. It uses **Go text/template** instead of Jinja2 for variable substitution.

## Design Goals

- Lightweight Ansible replacement – no Python dependency either on control node and managed node. On managed (remote) node it uses shell directly to execute task.
- Same INI inventory format and playbook YAML structure as Ansible
- Go text/template (`{{ .varname }}`) instead of Jinja2 (`{{ varname }}`)
- Compile-time extensible module system (no runtime plugins needed)
- SSH-based remote execution with agent/key/password auth
- Parallel host execution with configurable fork count

## Architecture

```
gosible/
├── main.go                        # Entry point – registers modules via blank imports
├── cmd/
│   ├── root.go                    # Cobra root command + shared flags
│   ├── run.go                     # `run` subcommand (ansible equivalent)
│   └── play.go                    # `play` subcommand (ansible-playbook equivalent)
├── internal/
│   ├── modules/
│   │   └── registry.go            # Module interface + compile-time registry
│   ├── inventory/
│   │   └── inventory.go           # Ansible INI inventory parser
│   ├── playbook/
│   │   └── playbook.go            # Playbook YAML parser + Go template rendering
│   ├── executor/
│   │   ├── executor.go            # SSH connection management
│   │   └── local.go               # Local command execution
│   ├── runner/
│   │   └── runner.go              # Play/task orchestration engine
│   └── output/
│       └── output.go              # Ansible-style colored terminal output
└── modules/
    └── shell/
        └── shell.go               # ansible.builtin.shell module
```

### Module System

Modules are registered at **compile time** via Go's `init()` mechanism. To add a new module:

1. Create a package under `modules/<name>/`
2. Implement the `modules.Module` interface
3. Call `modules.Register(&MyModule{})` in `init()`
4. Import it with a blank identifier in `main.go`:

```go
import _ "github.com/sagan/gosible/modules/shell"
```

The `Module` interface:

```go
type Module interface {
    Name() string           // FQCN: "ansible.builtin.shell"
    Aliases() []string      // Short names: ["shell"]
    Run(ctx *ModuleContext) (*Result, error)
}
```

## Installation

```bash
git clone https://github.com/sagan/gosible
cd gosible
go build -o gosible .
```

## Usage

### Ad-hoc commands (`run`)

Equivalent to `ansible`:

```bash
# Run a shell command on all hosts
gosible run all -m shell -a "uptime"

# Run on a specific group with sudo
gosible run webservers -m shell -a "systemctl restart nginx" --become

# Use a custom inventory
gosible run all -m shell -a "date" -i /etc/gosible/inventory
```

### Playbooks (`play`)

Equivalent to `ansible-playbook`:

```bash
# Run a playbook
gosible play site.yml

# Dry-run (check mode)
gosible play site.yml --check

# Extra variables
gosible play site.yml -e version=1.2.3

# List tasks without executing
gosible play site.yml --list-tasks

# List matching hosts
gosible play site.yml --list-hosts
```

## Playbook Format

gosible uses the same YAML format as Ansible:

```yaml
---
- name: My Play
  hosts: webservers
  become: true

  vars:
    app_version: "1.0.0"

  tasks:
    - name: Run a shell command
      shell: echo "Deploying version {{ .app_version }}"

    - name: Run with options
      ansible.builtin.shell:
        cmd: whoami
        chdir: /tmp

    - name: Conditional skip (creates)
      shell: setup.sh
      creates: /var/lib/app/.initialized

    - name: Register output
      shell: cat /etc/os-release
      register: os_info
```

**Variable substitution**: Use `{{ .varname }}` (Go template syntax). Variables from `--extra-vars` (`-e`) are substituted at parse time.

## Inventory Format

Standard Ansible INI format:

```ini
# Ungrouped hosts
web1.example.com ansible_user=ubuntu
web2.example.com

[webservers]
web[01:03].example.com

[webservers:vars]
ansible_user=ubuntu
ansible_ssh_private_key_file=~/.ssh/id_rsa

[dbservers]
db1.example.com ansible_port=2222

# Local execution
localhost ansible_connection=local
```

## Implemented Modules

| Module                  | Aliases | Description                       |
| ----------------------- | ------- | --------------------------------- |
| `ansible.builtin.shell` | `shell` | Execute shell commands on targets |

### ansible.builtin.shell Parameters

| Parameter             | Required | Description                       |
| --------------------- | -------- | --------------------------------- |
| `_raw_params` / `cmd` | yes      | The command to run                |
| `chdir`               | no       | Change directory before running   |
| `executable`          | no       | Shell to use (default: `/bin/sh`) |
| `creates`             | no       | Skip if this file exists          |
| `removes`             | no       | Skip if this file does NOT exist  |

## Adding New Modules

Example: implementing `ansible.builtin.copy`:

```go
// modules/copy/copy.go
package copy

import (
    "github.com/sagan/gosible/internal/modules"
)

func init() {
    modules.Register(&copyModule{})
}

type copyModule struct{}

func (m *copyModule) Name() string       { return "ansible.builtin.copy" }
func (m *copyModule) Aliases() []string  { return []string{"copy"} }
func (m *copyModule) Run(ctx *modules.ModuleContext) (*modules.Result, error) {
    // implementation...
}
```

Then in `main.go`:

```go
import _ "github.com/sagan/gosible/modules/copy"
```

## Testing

```
go test ./... -tags integration
```

## License

MIT
// gosible – A lightweight Ansible-compatible automation tool written in Go.
package main

import (
	"github.com/sagan/gosible/cmd"

	// Import modules to register them at startup (compile-time extensibility).
	// To add a new module, create a package under modules/ and import it here.
	_ "github.com/sagan/gosible/modules/shell"
	_ "github.com/sagan/gosible/modules/lineinfile"
	_ "github.com/sagan/gosible/modules/file"
	_ "github.com/sagan/gosible/modules/cron"
)

func main() {
	cmd.Execute()
}

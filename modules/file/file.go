// Package file implements the ansible.builtin.file module.
package file

import (
	"fmt"
	"strings"

	"github.com/sagan/gosible/internal/modules"
)

func init() {
	modules.Register(&fileModule{})
}

type fileModule struct{}

func (m *fileModule) Name() string    { return "ansible.builtin.file" }
func (m *fileModule) Aliases() []string { return []string{"file"} }

func (m *fileModule) Run(ctx *modules.ModuleContext) (*modules.Result, error) {
	args := ctx.Args
	path, ok := modules.StringArg(args, "path")
	if !ok {
		// Try 'dest' or 'name' as aliases for path
		path, ok = modules.StringArg(args, "dest")
		if !ok {
			path, ok = modules.StringArg(args, "name")
		}
	}
	if !ok || path == "" {
		return &modules.Result{Failed: true, Msg: "path is required"}, nil
	}

	state, _ := modules.StringArg(args, "state")
	mode, _ := modules.StringArg(args, "mode")
	owner, _ := modules.StringArg(args, "owner")
	group, _ := modules.StringArg(args, "group")
	src, _ := modules.StringArg(args, "src")
	recurse := modules.BoolArg(args, "recurse", false)

	// Get current status
	curType, curMode, curOwner, curGroup, exists, err := getFileStatus(ctx, path)
	if err != nil {
		return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to get file status: %v", err)}, nil
	}

	changed := false

	// Handle state
	switch state {
	case "absent":
		if exists {
			cmd := fmt.Sprintf("rm -rf %s", modules.ShellQuote(path))
			_, _, rc, err := ctx.Connection.RunCommand(cmd)
			if err != nil || rc != 0 {
				return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to remove: %v (rc=%d)", err, rc)}, nil
			}
			return &modules.Result{Changed: true, Msg: "removed"}, nil
		}
		return &modules.Result{Changed: false}, nil

	case "directory":
		if !exists {
			cmd := fmt.Sprintf("mkdir -p %s", modules.ShellQuote(path))
			_, _, rc, err := ctx.Connection.RunCommand(cmd)
			if err != nil || rc != 0 {
				return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to create directory: %v (rc=%d)", err, rc)}, nil
			}
			changed = true
			// Re-fetch status after creation
			curType, curMode, curOwner, curGroup, _, _ = getFileStatus(ctx, path)
		} else if curType != "directory" {
			return &modules.Result{Failed: true, Msg: fmt.Sprintf("path %s already exists and is not a directory", path)}, nil
		}

	case "touch":
		cmd := fmt.Sprintf("touch %s", modules.ShellQuote(path))
		_, _, rc, err := ctx.Connection.RunCommand(cmd)
		if err != nil || rc != 0 {
			return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to touch: %v (rc=%d)", err, rc)}, nil
		}
		changed = true
		// Re-fetch status
		curType, curMode, curOwner, curGroup, _, _ = getFileStatus(ctx, path)

	case "link":
		if src == "" {
			return &modules.Result{Failed: true, Msg: "src is required for state=link"}, nil
		}
		if !exists {
			cmd := fmt.Sprintf("ln -s %s %s", modules.ShellQuote(src), modules.ShellQuote(path))
			_, _, rc, err := ctx.Connection.RunCommand(cmd)
			if err != nil || rc != 0 {
				return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to create symlink: %v (rc=%d)", err, rc)}, nil
			}
			changed = true
		} else if curType != "symbolic link" {
			// In a real Ansible it might replace it, but for now we error or just skip.
			// Let's at least check if it points to the right place.
			// For simplicity, we'll just error if it exists and is not a link.
			return &modules.Result{Failed: true, Msg: fmt.Sprintf("path %s already exists and is not a symlink", path)}, nil
		}

	case "hard":
		if src == "" {
			return &modules.Result{Failed: true, Msg: "src is required for state=hard"}, nil
		}
		if !exists {
			cmd := fmt.Sprintf("ln %s %s", modules.ShellQuote(src), modules.ShellQuote(path))
			_, _, rc, err := ctx.Connection.RunCommand(cmd)
			if err != nil || rc != 0 {
				return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to create hardlink: %v (rc=%d)", err, rc)}, nil
			}
			changed = true
		}

	case "file":
		if !exists {
			return &modules.Result{Failed: true, Msg: fmt.Sprintf("path %s does not exist", path)}, nil
		}
		if curType == "directory" {
			return &modules.Result{Failed: true, Msg: fmt.Sprintf("path %s is a directory, not a file", path)}, nil
		}

	default:
		// Default is just to ensure path exists if any attribute is set
		if !exists && (mode != "" || owner != "" || group != "") {
			return &modules.Result{Failed: true, Msg: fmt.Sprintf("path %s does not exist", path)}, nil
		}
	}

	// Attributes (mode, owner, group)
	if exists || changed {
		attrChanged, err := applyAttributes(ctx, path, mode, owner, group, curMode, curOwner, curGroup, recurse)
		if err != nil {
			return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to apply attributes: %v", err)}, nil
		}
		if attrChanged {
			changed = true
		}
	}

	return &modules.Result{Changed: changed}, nil
}

func getFileStatus(ctx *modules.ModuleContext, path string) (fileType, mode, owner, group string, exists bool, err error) {
	// stat -c "%F %a %U %G"
	cmd := fmt.Sprintf("stat -c '%%F %%a %%U %%G' %s", modules.ShellQuote(path))
	stdout, _, rc, err := ctx.Connection.RunCommand(cmd)
	if err != nil {
		return "", "", "", "", false, err
	}
	if rc != 0 {
		return "", "", "", "", false, nil
	}

	fields := strings.Split(strings.TrimSpace(stdout), " ")
	if len(fields) < 4 {
		// Handle cases where %F might be multiple words like "regular file"
		// Actually stat -c "%F" returns "regular file" or "directory" etc.
		// Let's use a delimiter.
		cmd = fmt.Sprintf("stat -c '%%F|%%a|%%U|%%G' %s", modules.ShellQuote(path))
		stdout, _, rc, err = ctx.Connection.RunCommand(cmd)
		if err != nil || rc != 0 {
			return "", "", "", "", false, err
		}
		fields = strings.Split(strings.TrimSpace(stdout), "|")
		if len(fields) < 4 {
			return "", "", "", "", false, fmt.Errorf("unexpected stat output: %s", stdout)
		}
	}

	return fields[0], fields[1], fields[2], fields[3], true, nil
}

func applyAttributes(ctx *modules.ModuleContext, path, mode, owner, group, curMode, curOwner, curGroup string, recurse bool) (bool, error) {
	changed := false

	if owner != "" && owner != curOwner {
		cmd := "chown"
		if recurse {
			cmd += " -R"
		}
		cmd += fmt.Sprintf(" %s %s", modules.ShellQuote(owner), modules.ShellQuote(path))
		_, _, rc, err := ctx.Connection.RunCommand(cmd)
		if err != nil || rc != 0 {
			return false, fmt.Errorf("chown failed: %v (rc=%d)", err, rc)
		}
		changed = true
	}

	if group != "" && group != curGroup {
		cmd := "chgrp"
		if recurse {
			cmd += " -R"
		}
		cmd += fmt.Sprintf(" %s %s", modules.ShellQuote(group), modules.ShellQuote(path))
		_, _, rc, err := ctx.Connection.RunCommand(cmd)
		if err != nil || rc != 0 {
			return false, fmt.Errorf("chgrp failed: %v (rc=%d)", err, rc)
		}
		changed = true
	}

	if mode != "" {
		// Normalize mode: Ansible allows "0644" or "644".
		normMode := mode
		if strings.HasPrefix(normMode, "0") && len(normMode) > 3 {
			normMode = normMode[1:]
		}
		// Also normalize curMode (might have more than 3 digits if setuid/etc)
		normCurMode := curMode
		if len(normCurMode) > len(normMode) {
			normCurMode = normCurMode[len(normCurMode)-len(normMode):]
		}

		if normMode != normCurMode {
			cmd := "chmod"
			if recurse {
				cmd += " -R"
			}
			cmd += fmt.Sprintf(" %s %s", modules.ShellQuote(mode), modules.ShellQuote(path))
			_, _, rc, err := ctx.Connection.RunCommand(cmd)
			if err != nil || rc != 0 {
				return false, fmt.Errorf("chmod failed: %v (rc=%d)", err, rc)
			}
			changed = true
		}
	}

	return changed, nil
}

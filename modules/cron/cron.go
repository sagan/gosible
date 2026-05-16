// Package cron implements the ansible.builtin.cron module.
package cron

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/sagan/gosible/internal/modules"
)

func init() {
	modules.Register(&cronModule{})
}

type cronModule struct{}

func (m *cronModule) Name() string    { return "ansible.builtin.cron" }
func (m *cronModule) Aliases() []string { return []string{"cron"} }

func (m *cronModule) Run(ctx *modules.ModuleContext) (*modules.Result, error) {
	args := ctx.Args
	name, ok := modules.StringArg(args, "name")
	if !ok {
		return &modules.Result{Failed: true, Msg: "name is required"}, nil
	}

	job, _ := modules.StringArg(args, "job")
	state := "present"
	if s, ok := modules.StringArg(args, "state"); ok {
		state = s
	}

	user, _ := modules.StringArg(args, "user")
	disabled := modules.BoolArg(args, "disabled", false)

	// Time parameters
	minute := getArg(args, "minute", "*")
	hour := getArg(args, "hour", "*")
	day := getArg(args, "day", "*")
	month := getArg(args, "month", "*")
	weekday := getArg(args, "weekday", "*")
	specialTime, _ := modules.StringArg(args, "special_time")

	if state == "present" && job == "" {
		return &modules.Result{Failed: true, Msg: "job is required when state=present"}, nil
	}

	// 1. Get current crontab
	currentCrontab, err := getCrontab(ctx, user)
	if err != nil {
		return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to get crontab: %v", err)}, nil
	}

	// 2. Parse and update
	newCrontab, changed := updateCrontab(currentCrontab, name, job, state, minute, hour, day, month, weekday, specialTime, disabled)

	if !changed {
		return &modules.Result{Changed: false}, nil
	}

	// 3. Install new crontab
	err = setCrontab(ctx, user, newCrontab)
	if err != nil {
		return &modules.Result{Failed: true, Msg: fmt.Sprintf("failed to install crontab: %v", err)}, nil
	}

	return &modules.Result{Changed: true}, nil
}

func getArg(args modules.Args, key, defaultVal string) string {
	if v, ok := modules.StringArg(args, key); ok && v != "" {
		return v
	}
	return defaultVal
}

func getCrontab(ctx *modules.ModuleContext, user string) (string, error) {
	cmd := "crontab -l"
	if user != "" {
		cmd = fmt.Sprintf("crontab -u %s -l", modules.ShellQuote(user))
	}
	stdout, stderr, rc, err := ctx.Connection.RunCommand(cmd)
	if err != nil {
		return "", err
	}
	if rc != 0 {
		// Handle "no crontab for user"
		if strings.Contains(stderr, "no crontab") || strings.Contains(stdout, "no crontab") {
			return "", nil
		}
		return "", fmt.Errorf("crontab -l failed (rc=%d): %s", rc, stderr)
	}
	return stdout, nil
}

func setCrontab(ctx *modules.ModuleContext, user, content string) error {
	tmpPath := fmt.Sprintf("/tmp/gosible_cron_%d", time.Now().UnixNano())

	// Write content to temp file
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	writeCmd := fmt.Sprintf("printf '%%s' '%s' | base64 -d > %s", b64, tmpPath)
	_, _, rc, err := ctx.Connection.RunCommand(writeCmd)
	if err != nil || rc != 0 {
		return fmt.Errorf("failed to write temp crontab: %v (rc=%d)", err, rc)
	}
	defer func() {
		_, _, _, _ = ctx.Connection.RunCommand(fmt.Sprintf("rm -f %s", tmpPath))
	}()

	// Install crontab
	installCmd := fmt.Sprintf("crontab %s", tmpPath)
	if user != "" {
		installCmd = fmt.Sprintf("crontab -u %s %s", modules.ShellQuote(user), tmpPath)
	}
	_, _, rc, err = ctx.Connection.RunCommand(installCmd)
	if err != nil || rc != 0 {
		return fmt.Errorf("failed to install crontab: %v (rc=%d)", err, rc)
	}

	return nil
}

func updateCrontab(content, name, job, state, minute, hour, day, month, weekday, specialTime string, disabled bool) (string, bool) {
	lines := strings.Split(content, "\n")
	// Remove trailing empty line if it exists (Split adds it if content ends with \n)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	marker := fmt.Sprintf("# Ansible: %s", name)
	foundIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			foundIdx = i
			break
		}
	}

	newJobLine := ""
	if state == "present" {
		timePart := ""
		if specialTime != "" {
			if !strings.HasPrefix(specialTime, "@") {
				timePart = "@" + specialTime
			} else {
				timePart = specialTime
			}
		} else {
			timePart = fmt.Sprintf("%s %s %s %s %s", minute, hour, day, month, weekday)
		}
		newJobLine = fmt.Sprintf("%s %s", timePart, job)
		if disabled {
			newJobLine = "# " + newJobLine
		}
	}

	changed := false
	var resultLines []string

	if foundIdx != -1 {
		// Found existing entry. It's usually 2 lines: marker and job.
		if state == "absent" {
			// Remove marker and the next line (the job)
			resultLines = append(lines[:foundIdx], lines[foundIdx+2:]...)
			changed = true
		} else {
			// Update. Check if job line changed.
			if foundIdx+1 < len(lines) && lines[foundIdx+1] == newJobLine {
				return content, false // No change
			}
			// Replace the job line
			if foundIdx+1 < len(lines) {
				lines[foundIdx+1] = newJobLine
			} else {
				lines = append(lines, newJobLine)
			}
			resultLines = lines
			changed = true
		}
	} else {
		// Not found
		if state == "absent" {
			return content, false // Nothing to remove
		}
		// Add new entry
		resultLines = append(lines, marker, newJobLine)
		changed = true
	}

	newContent := strings.Join(resultLines, "\n")
	if len(resultLines) > 0 {
		newContent += "\n"
	}
	return newContent, changed
}

// Package output handles all terminal output for gosible, mimicking Ansible's
// familiar play recap and task result formatting.
package output

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sagan/gosible/internal/modules"
)

// Color escape codes for terminal output.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorWhite  = "\033[37m"
)

// Printer handles formatted output to stdout.
type Printer struct {
	mu      sync.Mutex
	noColor bool
}

// New creates a new Printer.
func New(noColor bool) *Printer {
	return &Printer{noColor: noColor}
}

func (p *Printer) color(c, s string) string {
	if p.noColor {
		return s
	}
	return c + s + colorReset
}

// PlayBanner prints the play name banner (e.g. "PLAY [Install web servers]").
func (p *Printer) PlayBanner(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	line := p.color(colorBold+colorWhite, fmt.Sprintf("PLAY [%s]", name))
	fmt.Printf("\n%s %s\n", line, strings.Repeat("*", max(0, 80-len(name)-8)))
}

// TaskBanner prints the task name banner.
func (p *Printer) TaskBanner(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	label := fmt.Sprintf("TASK [%s]", name)
	fmt.Printf("\n%s %s\n", p.color(colorBold, label), strings.Repeat("*", max(0, 80-len(label))))
}

// PrintResult prints a formatted task result for a single host.
func (p *Printer) PrintResult(result *modules.Result) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch {
	case result.Failed:
		tag := p.color(colorRed, "fatal")
		fmt.Printf("%s: [%s]: FAILED! => %s\n", tag, result.Host, p.formatResult(result))
	case result.Changed:
		tag := p.color(colorYellow, "changed")
		fmt.Printf("%s: [%s] => %s\n", tag, result.Host, p.formatResult(result))
	default:
		tag := p.color(colorGreen, "ok")
		fmt.Printf("%s: [%s] => %s\n", tag, result.Host, p.formatResult(result))
	}
}

// PrintSkipped prints a skip notice for a host.
func (p *Printer) PrintSkipped(host, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tag := p.color(colorCyan, "skipping")
	fmt.Printf("%s: [%s] => (reason: %s)\n", tag, host, reason)
}

// Recap prints the play recap summary table.
func (p *Printer) Recap(stats map[string]*HostStats) {
	p.mu.Lock()
	defer p.mu.Unlock()

	fmt.Printf("\n%s %s\n", p.color(colorBold+colorWhite, "PLAY RECAP"), strings.Repeat("*", 69))
	for host, s := range stats {
		ok := p.color(colorGreen, fmt.Sprintf("ok=%-4d", s.OK))
		changed := p.color(colorYellow, fmt.Sprintf("changed=%-4d", s.Changed))
		failed := p.color(colorRed, fmt.Sprintf("failed=%-4d", s.Failed))
		unreachable := p.color(colorRed, fmt.Sprintf("unreachable=%-4d", s.Unreachable))
		fmt.Printf("%-30s : %s %s %s %s\n", host, ok, changed, failed, unreachable)
	}
}

// formatResult produces a compact JSON-like result summary.
func (p *Printer) formatResult(r *modules.Result) string {
	parts := []string{fmt.Sprintf(`"msg": %q`, r.Msg)}
	if r.RC != 0 {
		parts = append(parts, fmt.Sprintf(`"rc": %d`, r.RC))
	}
	if r.Stdout != "" {
		parts = append(parts, fmt.Sprintf(`"stdout": %q`, truncate(r.Stdout, 1000)))
	}
	if r.Stderr != "" {
		parts = append(parts, fmt.Sprintf(`"stderr": %q`, truncate(r.Stderr, 1000)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// HostStats tracks aggregated result counts per host.
type HostStats struct {
	OK          int
	Changed     int
	Failed      int
	Unreachable int
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

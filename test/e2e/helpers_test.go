// Package e2e contains integration tests that compare gosible's behavior
// against the real ansible-playbook command.
//
// These tests require:
//   - A compiled gosible binary (built via go build in the project root)
//   - ansible-playbook installed and available in PATH
//
// Run with:
//
//	go test -v -tags integration ./test/e2e/...
//
//go:build integration

package e2e

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────
// Test harness helpers
// ────────────────────────────────────────────────────────────────

// projectRoot returns the absolute path to the gosible project root,
// derived from the location of this test file.
func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine source file location")
	}
	// file is .../test/e2e/suite_test.go → go up two levels
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// gosibleBin returns the path to the compiled gosible binary.
// It builds the binary on first call and caches the result in a temp dir.
func gosibleBin(t *testing.T) string {
	t.Helper()
	root := projectRoot(t)
	bin := filepath.Join(root, "gosible_e2e_test_bin")
	// Build once per test run.
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build gosible: %v\n%s", err, out)
	}
	t.Cleanup(func() { os.Remove(bin) })
	return bin
}

// localInventory returns a path to a temp inventory file containing only localhost
// with ansible_connection=local, compatible with both gosible and ansible-playbook.
func localInventory(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "inventory-*.ini")
	if err != nil {
		t.Fatalf("create inventory: %v", err)
	}
	fmt.Fprintln(f, "localhost ansible_connection=local")
	f.Close()
	return f.Name()
}

// fixtureFile returns the absolute path to a test fixture playbook.
func fixtureFile(t *testing.T, name string) string {
	t.Helper()
	root := projectRoot(t)
	p := filepath.Join(root, "test", "e2e", "testdata", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture not found: %s", p)
	}
	return p
}

// RunResult captures the outcome of running a CLI command.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Combined string
}

// runGosible executes the gosible binary with the given arguments and returns its output.
func runGosible(t *testing.T, bin string, args ...string) RunResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rc := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			t.Logf("gosible exec error (non-exit): %v", err)
		}
	}
	out := stdout.String()
	errOut := stderr.String()
	return RunResult{
		ExitCode: rc,
		Stdout:   out,
		Stderr:   errOut,
		Combined: out + errOut,
	}
}

// runAnsiblePlaybook executes ansible-playbook with the given arguments.
func runAnsiblePlaybook(t *testing.T, args ...string) RunResult {
	t.Helper()
	// ansible-playbook needs ANSIBLE_PYTHON_INTERPRETER set to avoid warnings
	// that pollute the recap output.
	cmd := exec.Command("ansible-playbook", args...)
	cmd.Env = append(os.Environ(),
		"ANSIBLE_PYTHON_INTERPRETER=auto_silent",
		"ANSIBLE_FORCE_COLOR=0",
		"ANSIBLE_NOCOLOR=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rc := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			t.Logf("ansible-playbook exec error: %v", err)
		}
	}
	out := stdout.String()
	errOut := stderr.String()
	return RunResult{
		ExitCode: rc,
		Stdout:   out,
		Stderr:   errOut,
		Combined: out + errOut,
	}
}

// ────────────────────────────────────────────────────────────────
// Play Recap parsing
// ────────────────────────────────────────────────────────────────

// HostRecap holds the per-host summary from a play recap.
type HostRecap struct {
	Host        string
	OK          int
	Changed     int
	Unreachable int
	Failed      int
	// Skipped is only populated for ansible-playbook output.
	Skipped int
}

// PlayRecap maps host name → HostRecap for all hosts in the recap.
type PlayRecap map[string]HostRecap

// recapLineRe matches both gosible and ansible-playbook recap lines.
// Gosible format:  localhost       : ok=0    changed=2    failed=0    unreachable=0
// Ansible format:  localhost       : ok=4    changed=3    unreachable=0    failed=0    skipped=0 ...
var recapLineRe = regexp.MustCompile(
	`^(\S+)\s*:\s*` +
		`ok=(\d+)\s+` +
		`changed=(\d+)\s+` +
		`(?:unreachable=(\d+)\s+)?` +
		`failed=(\d+)` +
		`(?:\s+unreachable=(\d+))?` + // ansible puts unreachable after failed
		`.*$`,
)

// parseRecap extracts the PLAY RECAP section from combined output.
func parseRecap(output string) PlayRecap {
	recap := PlayRecap{}
	inRecap := false
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "PLAY RECAP") {
			inRecap = true
			continue
		}
		if !inRecap {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := recapLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		host := m[1]
		ok := atoi(m[2])
		changed := atoi(m[3])
		// unreachable appears at position 4 in gosible, position 6 in ansible
		unreachable := atoi(m[4])
		if unreachable == 0 {
			unreachable = atoi(m[6])
		}
		failed := atoi(m[5])

		recap[host] = HostRecap{
			Host:        host,
			OK:          ok,
			Changed:     changed,
			Unreachable: unreachable,
			Failed:      failed,
		}
	}
	return recap
}

func atoi(s string) int {
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// ────────────────────────────────────────────────────────────────
// Assertion helpers
// ────────────────────────────────────────────────────────────────

// assertRecapsEqual compares the play recaps from gosible and ansible-playbook.
//
// The ok/changed semantics differ between the two tools:
//   - Ansible: ok = ALL tasks that didn't fail (includes changed tasks)
//              changed = subset of ok tasks that made changes
//   - Gosible: ok = tasks that were unchanged/skipped
//              changed = tasks that ran and made changes
//
// Therefore we compare:
//   1. failed     – must be identical
//   2. unreachable – must be identical
//   3. non-failed total (ok+changed) – must be identical (represents tasks that succeeded)
func assertRecapsEqual(t *testing.T, gosibleRecap, ansibleRecap PlayRecap) {
	t.Helper()
	for host, ar := range ansibleRecap {
		gr, ok := gosibleRecap[host]
		if !ok {
			t.Errorf("host %q present in ansible recap but missing from gosible recap", host)
			continue
		}

		// In ansible: ok includes changed. In gosible: ok and changed are exclusive.
		// Normalize: non-failed = ok + changed for gosible; ok for ansible (since ansible ok⊇changed).
		// But ansible also uses ok as the supercount, so:
		//   ansible non-failed = ar.OK  (ansible ok already counts all non-failed)
		//   gosible non-failed = gr.OK + gr.Changed
		gosibleNonFailed := gr.OK + gr.Changed
		ansibleNonFailed := ar.OK // ansible ok is the superset (includes changed)

		if gosibleNonFailed != ansibleNonFailed {
			t.Errorf("host %q: non-failed task count mismatch: gosible(ok+changed)=%d ansible(ok)=%d\n"+
				"  gosible: ok=%d changed=%d failed=%d unreachable=%d\n"+
				"  ansible: ok=%d changed=%d failed=%d unreachable=%d",
				host,
				gosibleNonFailed, ansibleNonFailed,
				gr.OK, gr.Changed, gr.Failed, gr.Unreachable,
				ar.OK, ar.Changed, ar.Failed, ar.Unreachable,
			)
		}
		if gr.Failed != ar.Failed {
			t.Errorf("host %q: failed count mismatch: gosible=%d ansible=%d", host, gr.Failed, ar.Failed)
		}
		if gr.Unreachable != ar.Unreachable {
			t.Errorf("host %q: unreachable count mismatch: gosible=%d ansible=%d", host, gr.Unreachable, ar.Unreachable)
		}
	}
	for host := range gosibleRecap {
		if _, ok := ansibleRecap[host]; !ok {
			t.Errorf("host %q present in gosible recap but missing from ansible recap", host)
		}
	}
}

// assertExitCodesEqual checks that both commands agree on success vs failure.
func assertExitCodesEqual(t *testing.T, gosibleRC, ansibleRC int, label string) {
	t.Helper()
	gosibleFailed := gosibleRC != 0
	ansibleFailed := ansibleRC != 0
	if gosibleFailed != ansibleFailed {
		t.Errorf("%s: exit code agreement mismatch: gosible rc=%d (failed=%v) ansible rc=%d (failed=%v)",
			label, gosibleRC, gosibleFailed, ansibleRC, ansibleFailed)
	}
}

// assertOutputContains checks that the gosible output contains the expected substring.
func assertOutputContains(t *testing.T, output, want, label string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Errorf("%s: expected output to contain %q\ngot:\n%s", label, want, output)
	}
}

// assertOutputNotContains checks that output does NOT contain an unwanted string.
func assertOutputNotContains(t *testing.T, output, notWant, label string) {
	t.Helper()
	if strings.Contains(output, notWant) {
		t.Errorf("%s: expected output NOT to contain %q\ngot:\n%s", label, notWant, output)
	}
}

// ────────────────────────────────────────────────────────────────
// runBoth is the core helper: run both tools against the same fixture
// and return their results for comparison.
// ────────────────────────────────────────────────────────────────

func runBoth(t *testing.T, bin, inventory, fixture string, extraArgs ...string) (gosibleResult, ansibleResult RunResult) {
	t.Helper()

	gosibleArgs := append([]string{"play", fixture, "-i", inventory, "--no-color"}, extraArgs...)
	ansibleArgs := append([]string{fixture, "-i", inventory}, extraArgs...)

	t.Logf("gosible: %s %s", bin, strings.Join(gosibleArgs, " "))
	t.Logf("ansible: ansible-playbook %s", strings.Join(ansibleArgs, " "))

	gosibleResult = runGosible(t, bin, gosibleArgs...)
	ansibleResult = runAnsiblePlaybook(t, ansibleArgs...)

	t.Logf("=== gosible stdout ===\n%s", gosibleResult.Stdout)
	t.Logf("=== ansible stdout ===\n%s", ansibleResult.Stdout)

	return gosibleResult, ansibleResult
}

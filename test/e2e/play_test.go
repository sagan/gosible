// Package e2e contains integration tests that compare gosible's behavior
// against the real ansible-playbook command.
//
//go:build integration

package e2e

import (
	"os"
	"strings"
	"testing"
)

// setupSuite verifies the test prerequisites once per package.
func TestMain(m *testing.M) {
	if _, err := os.Stat("/usr/bin/ansible-playbook"); err != nil {
		if _, err2 := os.LookupEnv("ANSIBLE_PLAYBOOK"); err2 {
			os.Exit(0) // skip all if ansible not installed
		}
	}
	os.Exit(m.Run())
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ShellBasic – free-form shell execution
// ─────────────────────────────────────────────────────────────

func TestPlay_ShellBasic(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_basic.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "shell_basic exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// Gosible should have executed all three tasks and captured stdout.
	assertOutputContains(t, gr.Stdout, "hello gosible", "gosible stdout")
	assertOutputContains(t, gr.Stdout, "one two three", "gosible stdout")

	// Both tools should show "changed" for shell tasks.
	gRecap := parseRecap(gr.Stdout)
	if h, ok := gRecap["localhost"]; ok {
		if h.Changed == 0 {
			t.Errorf("expected at least 1 changed task, got changed=%d", h.Changed)
		}
	} else {
		t.Error("localhost not found in gosible recap")
	}
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ShellChdir – chdir changes working directory
// ─────────────────────────────────────────────────────────────

func TestPlay_ShellChdir(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_chdir.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "shell_chdir exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// gosible must change to /tmp and /var respectively.
	assertOutputContains(t, gr.Stdout, "/tmp", "gosible chdir /tmp")
	assertOutputContains(t, gr.Stdout, "/var", "gosible chdir /var")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ShellCreates – creates skips task when file exists
// ─────────────────────────────────────────────────────────────

func TestPlay_ShellCreates(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_creates.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "shell_creates exit code")

	// The "creates: /etc/hosts" task must be skipped – "should be skipped"
	// must NOT appear in gosible stdout.
	assertOutputNotContains(t, gr.Stdout, "should be skipped", "gosible creates skip")

	// The second task (creates: /nonexistent/...) must run.
	assertOutputContains(t, gr.Stdout, "ran because file missing", "gosible creates run")

	// Total task count must match ansible.
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ShellRemoves – removes skips task when file is absent
// ─────────────────────────────────────────────────────────────

func TestPlay_ShellRemoves(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_removes.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "shell_removes exit code")

	// The "removes: /nonexistent/..." task must be skipped.
	assertOutputNotContains(t, gr.Stdout, "should be skipped", "gosible removes skip")

	// The "removes: /etc/hosts" task must run.
	assertOutputContains(t, gr.Stdout, "ran because file exists", "gosible removes run")

	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ShellRC – return code marks task as failed
// ─────────────────────────────────────────────────────────────

func TestPlay_ShellRC(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_rc.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	// Both should exit 0 because failing tasks have ignore_errors=true.
	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "shell_rc exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// Play recap should show 0 failed (all failures ignored).
	gRecap := parseRecap(gr.Stdout)
	if h, ok := gRecap["localhost"]; ok {
		if h.Failed != 0 {
			t.Errorf("expected failed=0 (all errors ignored), got failed=%d", h.Failed)
		}
	} else {
		t.Error("localhost not found in gosible recap")
	}
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ShellFailStopsPlay – unhandled failure stops the play
// ─────────────────────────────────────────────────────────────

func TestPlay_ShellFailStopsPlay(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_fail_stops.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	// Both should exit non-zero when a task fails without ignore_errors.
	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "shell_fail exit code")

	// Both should report failed=1.
	gRecap := parseRecap(gr.Stdout)
	aRecap := parseRecap(ar.Stdout)
	if h, ok := gRecap["localhost"]; ok {
		if h.Failed == 0 {
			t.Errorf("gosible: expected failed>0, got failed=%d", h.Failed)
		}
	}
	if h, ok := aRecap["localhost"]; ok {
		if h.Failed == 0 {
			t.Errorf("ansible: expected failed>0, got failed=%d", h.Failed)
		}
	}

	// The unreachable task must NOT have produced output in gosible.
	assertOutputNotContains(t, gr.Stdout, "unreachable task", "gosible stops after failure")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_MultiTask – task ordering is preserved
// ─────────────────────────────────────────────────────────────

func TestPlay_MultiTask(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "multi_task.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "multi_task exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// Verify each step ran and output appeared in order.
	for _, step := range []string{"step-1", "step-2", "step-3"} {
		assertOutputContains(t, gr.Stdout, step, "gosible task ordering")
	}

	// Verify ordering: step-1 must appear before step-2, step-2 before step-3.
	idx1 := strings.Index(gr.Stdout, "step-1")
	idx2 := strings.Index(gr.Stdout, "step-2")
	idx3 := strings.Index(gr.Stdout, "step-3")
	if !(idx1 < idx2 && idx2 < idx3) {
		t.Errorf("task output order wrong: step-1@%d step-2@%d step-3@%d", idx1, idx2, idx3)
	}

	// Multi-line output must be captured.
	assertOutputContains(t, gr.Stdout, "line1", "gosible multi-line stdout")
	assertOutputContains(t, gr.Stdout, "line2", "gosible multi-line stdout")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ListTasks – --list-tasks must not execute anything
// ─────────────────────────────────────────────────────────────

func TestPlay_ListTasks(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "multi_task.yml")

	result := runGosible(t, bin, "play", fixture, "-i", inv, "--no-color", "--list-tasks")

	if result.ExitCode != 0 {
		t.Errorf("--list-tasks should exit 0, got %d\n%s", result.ExitCode, result.Combined)
	}

	// Must list the task names.
	for _, task := range []string{"First task", "Second task", "Third task"} {
		assertOutputContains(t, result.Stdout, task, "--list-tasks output")
	}

	// Must NOT have actually executed any commands.
	assertOutputNotContains(t, result.Stdout, "step-1", "--list-tasks must not execute")
	assertOutputNotContains(t, result.Stdout, "PLAY RECAP", "--list-tasks must not show recap")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ListHosts – --list-hosts must not execute anything
// ─────────────────────────────────────────────────────────────

func TestPlay_ListHosts(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "multi_task.yml")

	result := runGosible(t, bin, "play", fixture, "-i", inv, "--no-color", "--list-hosts")

	if result.ExitCode != 0 {
		t.Errorf("--list-hosts should exit 0, got %d\n%s", result.ExitCode, result.Combined)
	}

	// Must list the host.
	assertOutputContains(t, result.Stdout, "localhost", "--list-hosts output")

	// Must NOT have executed any commands or shown a recap.
	assertOutputNotContains(t, result.Stdout, "step-1", "--list-hosts must not execute")
	assertOutputNotContains(t, result.Stdout, "PLAY RECAP", "--list-hosts must not show recap")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ExitCodeOnSuccess – successful playbook exits 0
// ─────────────────────────────────────────────────────────────

func TestPlay_ExitCodeOnSuccess(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_basic.yml")

	gr := runGosible(t, bin, "play", fixture, "-i", inv, "--no-color")
	if gr.ExitCode != 0 {
		t.Errorf("successful playbook should exit 0, got %d\n%s", gr.ExitCode, gr.Combined)
	}
}

// ─────────────────────────────────────────────────────────────
// TestPlay_ExitCodeOnFailure – playbook with unhandled failure exits non-zero
// ─────────────────────────────────────────────────────────────

func TestPlay_ExitCodeOnFailure(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_fail_stops.yml")

	gr := runGosible(t, bin, "play", fixture, "-i", inv, "--no-color")
	if gr.ExitCode == 0 {
		t.Errorf("playbook with unhandled failure should exit non-zero, got 0\n%s", gr.Combined)
	}
}

// ─────────────────────────────────────────────────────────────
// TestPlay_PlayBannerPresent – play name appears in output
// ─────────────────────────────────────────────────────────────

func TestPlay_PlayBannerPresent(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "shell_basic.yml")

	gr := runGosible(t, bin, "play", fixture, "-i", inv, "--no-color")
	assertOutputContains(t, gr.Stdout, "PLAY [Shell Basic]", "gosible play banner")
	assertOutputContains(t, gr.Stdout, "TASK [Echo a string]", "gosible task banner")
	assertOutputContains(t, gr.Stdout, "PLAY RECAP", "gosible play recap banner")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_LineinfileBasic – lineinfile module functionality
// ─────────────────────────────────────────────────────────────

func TestPlay_LineinfileBasic(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "lineinfile_basic.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "lineinfile_basic exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// Verify some content in the output.
	assertOutputContains(t, gr.Stdout, "backrefed second line", "gosible lineinfile backref")
	assertOutputContains(t, gr.Stdout, "inserted at beginning", "gosible lineinfile BOF")
	assertOutputContains(t, gr.Stdout, "brand new line", "gosible lineinfile create")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_LineinfileAdvanced – backup and validate
// ─────────────────────────────────────────────────────────────

func TestPlay_LineinfileAdvanced(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "lineinfile_advanced.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "lineinfile_advanced exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// Gosible should report changed for the successful tasks and failed for the validation failure.
	// Since ignore_errors: yes is used, the play should continue.
}

// ─────────────────────────────────────────────────────────────
// TestPlay_FileBasic – file module functionality
// ─────────────────────────────────────────────────────────────

func TestPlay_FileBasic(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "file_basic.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "file_basic exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// Verify stat output in gosible stdout.
	assertOutputContains(t, gr.Stdout, "600 regular", "gosible file stat")
}

// ─────────────────────────────────────────────────────────────
// TestPlay_CronBasic – cron module functionality
// ─────────────────────────────────────────────────────────────

func TestPlay_CronBasic(t *testing.T) {
	bin := gosibleBin(t)
	inv := localInventory(t)
	fixture := fixtureFile(t, "cron_basic.yml")

	gr, ar := runBoth(t, bin, inv, fixture)

	assertExitCodesEqual(t, gr.ExitCode, ar.ExitCode, "cron_basic exit code")
	assertRecapsEqual(t, parseRecap(gr.Stdout), parseRecap(ar.Stdout))

	// Verify crontab output contains our special time job during the run.
	assertOutputContains(t, gr.Stdout, "Ansible: reboot job", "gosible crontab content")
}

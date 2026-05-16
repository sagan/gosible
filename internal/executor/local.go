package executor

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// localRun executes a shell command on the local machine using /bin/sh.
func localRun(cmd string) (stdout, stderr string, rc int, err error) {
	c := exec.Command("/bin/sh", "-c", cmd)
	var outBuf, errBuf bytes.Buffer
	c.Stdout = &outBuf
	c.Stderr = &errBuf

	runErr := c.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return stdout, stderr, exitErr.ExitCode(), nil
		}
		return stdout, stderr, -1, fmt.Errorf("local run: %w", runErr)
	}
	return stdout, stderr, 0, nil
}

// cappedBuffer is a bytes.Buffer used for capturing command output.
type cappedBuffer struct {
	buf bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (n int, err error) {
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string {
	return strings.TrimRight(b.buf.String(), "\n")
}

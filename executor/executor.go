package executor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pheckel/noshell/config"
)

// Executor runs allowlisted commands with a per-call timeout. The
// globalTimeout is used unless the matched rule sets a per-command override.
type Executor struct {
	globalTimeout time.Duration
}

func New(globalTimeout time.Duration) *Executor {
	return &Executor{globalTimeout: globalTimeout}
}

// Execute runs input as `<cmd> <args...>`, piping stdout/stderr to the
// terminal. Returns the child exit code, 124 on timeout, or 1 on other
// fork/exec errors.
func (e *Executor) Execute(input string, rule *config.CommandRule) int {
	cmd, argsStr := splitCommand(input)

	var args []string
	if argsStr != "" {
		args = strings.Fields(argsStr)
	}

	timeout := e.globalTimeout
	if rule.Timeout > 0 {
		timeout = rule.Timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := exec.CommandContext(ctx, cmd, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			os.Stderr.WriteString("noshell: command timed out\n")
			return 124
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}

	return 0
}

func splitCommand(input string) (string, string) {
	input = strings.TrimSpace(input)
	idx := strings.IndexByte(input, ' ')
	if idx == -1 {
		return input, ""
	}
	return input[:idx], input[idx+1:]
}

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Execute runs the given command with timeout, piping stdout/stderr to the terminal.
// Returns the exit code.
func Execute(input string, rule *CommandRule, globalTimeout time.Duration) int {
	cmd, argsStr := splitCommand(input)

	var args []string
	if argsStr != "" {
		args = strings.Fields(argsStr)
	}

	timeout := globalTimeout
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

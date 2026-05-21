package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	osexec "os/exec"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/util"
)

// Execute runs a single command with optional stdin. ExitCode is the
// child's exit, timeoutExitCode on deadline, or 1 on fork/exec errors.
func Execute(path string, argv []string, rule *config.CommandRule, stdin io.Reader) *Result {
	timeout := defaultTimeout
	if rule != nil && rule.Timeout > 0 {
		timeout = rule.Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	stdout := util.NewCappedBuffer(maxOutputBytes)
	stderr := util.NewCappedBuffer(maxOutputBytes)

	c := osexec.CommandContext(ctx, path, argv...)
	c.Stdout = stdout
	c.Stderr = stderr
	if stdin != nil {
		c.Stdin = stdin
	}

	err := c.Run()
	return finalize(&Result{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Truncated: stdout.Truncated() || stderr.Truncated(),
	}, ctx, err)
}

// finalize fills in ExitCode/TimedOut on a Result based on the context
// state and the run error from os/exec. Shared by Execute and
// ExecutePipeline so the timeout/exit-code mapping stays in one place.
func finalize(res *Result, ctx context.Context, err error) *Result {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.ExitCode = timeoutExitCode
		res.TimedOut = true
		return res
	}
	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res
	}
	if err != nil {
		res.ExitCode = 1
	}
	return res
}

// ExecutePipeline runs N stages with stage i's stdout wired to stage
// i+1's stdin. All stages share one deadline (max of defaultTimeout and
// any per-rule timeout). Only the last stage's stdout is returned; stderr
// from every stage is merged. Exit code is the last stage's exit.
func ExecutePipeline(stages []*Stage) *Result {
	if len(stages) == 0 {
		return &Result{ExitCode: 1, Stderr: []byte("rrsh: empty pipeline\n")}
	}
	if len(stages) == 1 {
		s := stages[0]
		return Execute(s.Path, s.Argv, s.Rule, s.Stdin)
	}

	timeout := defaultTimeout
	for _, s := range stages {
		if s.Rule != nil && s.Rule.Timeout > timeout {
			timeout = s.Rule.Timeout
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmds := make([]*osexec.Cmd, len(stages))
	stderrs := make([]*util.CappedBuffer, len(stages))
	for i, s := range stages {
		cmds[i] = osexec.CommandContext(ctx, s.Path, s.Argv...)
		stderrs[i] = util.NewCappedBuffer(maxOutputBytes)
		cmds[i].Stderr = stderrs[i]
	}
	if stages[0].Stdin != nil {
		cmds[0].Stdin = stages[0].Stdin
	}
	for i := 0; i < len(cmds)-1; i++ {
		pipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return &Result{ExitCode: 1, Stderr: []byte("rrsh: pipeline setup failed\n")}
		}
		cmds[i+1].Stdin = pipe
	}
	finalOut := util.NewCappedBuffer(maxOutputBytes)
	cmds[len(cmds)-1].Stdout = finalOut

	for _, c := range cmds {
		if err := c.Start(); err != nil {
			// Kill anything already started, best effort.
			for _, started := range cmds {
				if started.Process != nil {
					_ = started.Process.Kill()
				}
			}
			return &Result{ExitCode: 1, Stderr: []byte("rrsh: pipeline start failed: " + err.Error() + "\n")}
		}
	}

	var lastErr error
	for i, c := range cmds {
		if err := c.Wait(); err != nil && i == len(cmds)-1 {
			lastErr = err
		}
	}

	var mergedErr bytes.Buffer
	truncated := finalOut.Truncated()
	for _, s := range stderrs {
		mergedErr.Write(s.Bytes())
		if s.Truncated() {
			truncated = true
		}
	}
	return finalize(&Result{
		Stdout:    finalOut.Bytes(),
		Stderr:    mergedErr.Bytes(),
		Truncated: truncated,
	}, ctx, lastErr)
}

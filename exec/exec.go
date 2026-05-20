// Package exec runs allowlisted commands. It operates on argv slices,
// not shell strings — there is no tokenization in this package.
//
// Two entry points:
//   - Execute runs a single (path, argv) with optional stdin.
//   - ExecutePipeline wires N stages together via native Go pipes.
//
// Output is captured into bounded buffers. Anything past maxOutputBytes
// per stream is dropped and Result.Truncated is set.
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

// Execer runs allowlisted commands. There is no per-instance state
// today — the type exists so future hooks (rate limiting, audit
// streams, etc.) can attach via methods without changing the call site.
type Execer struct{}

// New constructs an Execer.
func New() *Execer {
	return &Execer{}
}

// Execute runs a single command. Stdin may be nil. The returned Result
// always has Stdout/Stderr populated (possibly empty); ExitCode is the
// child's exit, timeoutExitCode on deadline, or 1 on fork/exec errors.
// Returns a pointer so callers can mutate the result in place when
// merging with elevation/pipeline wrappers.
func (e *Execer) Execute(path string, argv []string, rule *config.CommandRule, stdin io.Reader) *Result {
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
	res := &Result{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Truncated: stdout.Truncated() || stderr.Truncated(),
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.ExitCode = timeoutExitCode
		res.TimedOut = true
		return res
	}
	if err != nil {
		var exitErr *osexec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res
		}
		res.ExitCode = 1
		return res
	}
	res.ExitCode = 0
	return res
}

// ExecutePipeline runs N stages with the stdout of stage i wired to the
// stdin of stage i+1. All stages share one context whose timeout is the
// maximum of defaultTimeout and each stage's per-rule timeout.
//
// Only the last stage's stdout is captured into Result.Stdout. Stderr from
// every stage is merged into Result.Stderr (in start order, but interleaving
// is unavoidable since stages run concurrently). The exit code is the last
// stage's exit; earlier non-zero exits do not override.
func (e *Execer) ExecutePipeline(stages []Stage) *Result {
	if len(stages) == 0 {
		return &Result{ExitCode: 1, Stderr: []byte("rrsh: empty pipeline\n")}
	}
	if len(stages) == 1 {
		s := stages[0]
		return e.Execute(s.Path, s.Argv, s.Rule, s.Stdin)
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
	for i, s := range stages {
		cmds[i] = osexec.CommandContext(ctx, s.Path, s.Argv...)
	}

	stderrs := make([]*util.CappedBuffer, len(stages))
	for i := range stderrs {
		stderrs[i] = util.NewCappedBuffer(maxOutputBytes)
		cmds[i].Stderr = stderrs[i]
	}

	if stages[0].Stdin != nil {
		cmds[0].Stdin = stages[0].Stdin
	}

	// Wire stage i's stdout to stage i+1's stdin.
	for i := 0; i < len(cmds)-1; i++ {
		pipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return &Result{ExitCode: 1, Stderr: []byte("rrsh: pipeline setup failed\n")}
		}
		cmds[i+1].Stdin = pipe
	}

	finalOut := util.NewCappedBuffer(maxOutputBytes)
	cmds[len(cmds)-1].Stdout = finalOut

	// Start all stages.
	for _, c := range cmds {
		if err := c.Start(); err != nil {
			// Best effort: kill anything already started.
			for _, started := range cmds {
				if started.Process != nil {
					_ = started.Process.Kill()
				}
			}
			return &Result{ExitCode: 1, Stderr: []byte("rrsh: pipeline start failed: " + err.Error() + "\n")}
		}
	}

	// Wait for every stage; capture final exit from the last.
	var lastErr error
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			if i == len(cmds)-1 {
				lastErr = err
			}
		}
	}

	res := &Result{
		Stdout: finalOut.Bytes(),
	}
	var mergedErr bytes.Buffer
	truncated := finalOut.Truncated()
	for i := range stderrs {
		mergedErr.Write(stderrs[i].Bytes())
		if stderrs[i].Truncated() {
			truncated = true
		}
	}
	res.Stderr = mergedErr.Bytes()
	res.Truncated = truncated

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.ExitCode = timeoutExitCode
		res.TimedOut = true
		return res
	}
	if lastErr != nil {
		var exitErr *osexec.ExitError
		if errors.As(lastErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res
		}
		res.ExitCode = 1
		return res
	}
	return res
}

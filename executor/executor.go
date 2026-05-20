// Package executor runs allowlisted commands. It operates on argv slices,
// not shell strings — there is no tokenization in this package.
//
// Two entry points:
//   - Execute runs a single (path, argv) with optional stdin.
//   - ExecutePipeline wires N stages together via native Go pipes.
//
// Output is captured into bounded buffers. Anything past MaxOutputBytes
// per stream is dropped and Result.Truncated is set.
package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/binwiederhier/rrsh/config"
)

// MaxOutputBytes is the per-stream (stdout/stderr) cap on captured output.
// Output beyond this is silently dropped and the Result is marked Truncated.
const MaxOutputBytes = 10 * 1024 * 1024 // 10 MB

// TimeoutExitCode is returned when the context deadline fires before the
// command (or pipeline) finishes. Matches shell convention.
const TimeoutExitCode = 124

// Result is the structured outcome of running a command or pipeline.
type Result struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	TimedOut  bool
	Truncated bool
}

// Stage is one segment of a pipeline.
type Stage struct {
	Path string
	Argv []string
	Rule *config.CommandRule
	// Stdin, if non-nil, is wired to the first stage's stdin. Ignored on
	// stages other than index 0.
	Stdin io.Reader
}

// Executor runs allowlisted commands with a per-call timeout. The
// globalTimeout is used unless the matched rule sets a per-command override.
type Executor struct {
	globalTimeout time.Duration
}

func New(globalTimeout time.Duration) *Executor {
	return &Executor{globalTimeout: globalTimeout}
}

// Execute runs a single command. Stdin may be nil. The returned Result
// always has Stdout/Stderr populated (possibly empty); ExitCode is the
// child's exit, TimeoutExitCode on deadline, or 1 on fork/exec errors.
func (e *Executor) Execute(path string, argv []string, rule *config.CommandRule, stdin io.Reader) Result {
	timeout := e.globalTimeout
	if rule != nil && rule.Timeout > 0 {
		timeout = rule.Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr cappedBuffer
	stdout.limit = MaxOutputBytes
	stderr.limit = MaxOutputBytes

	c := exec.CommandContext(ctx, path, argv...)
	c.Stdout = &stdout
	c.Stderr = &stderr
	if stdin != nil {
		c.Stdin = stdin
	}

	err := c.Run()
	res := Result{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Truncated: stdout.truncated || stderr.truncated,
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.ExitCode = TimeoutExitCode
		res.TimedOut = true
		return res
	}
	if err != nil {
		var exitErr *exec.ExitError
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
// maximum of the global timeout and each stage's per-rule timeout.
//
// Only the last stage's stdout is captured into Result.Stdout. Stderr from
// every stage is merged into Result.Stderr (in start order, but interleaving
// is unavoidable since stages run concurrently). The exit code is the last
// stage's exit; earlier non-zero exits do not override.
func (e *Executor) ExecutePipeline(stages []Stage) Result {
	if len(stages) == 0 {
		return Result{ExitCode: 1, Stderr: []byte("rrsh: empty pipeline\n")}
	}
	if len(stages) == 1 {
		s := stages[0]
		return e.Execute(s.Path, s.Argv, s.Rule, s.Stdin)
	}

	timeout := e.globalTimeout
	for _, s := range stages {
		if s.Rule != nil && s.Rule.Timeout > timeout {
			timeout = s.Rule.Timeout
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmds := make([]*exec.Cmd, len(stages))
	for i, s := range stages {
		cmds[i] = exec.CommandContext(ctx, s.Path, s.Argv...)
	}

	stderrs := make([]cappedBuffer, len(stages))
	for i := range stderrs {
		stderrs[i].limit = MaxOutputBytes
		cmds[i].Stderr = &stderrs[i]
	}

	if stages[0].Stdin != nil {
		cmds[0].Stdin = stages[0].Stdin
	}

	// Wire stage i's stdout to stage i+1's stdin.
	for i := 0; i < len(cmds)-1; i++ {
		pipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return Result{ExitCode: 1, Stderr: []byte("rrsh: pipeline setup failed\n")}
		}
		cmds[i+1].Stdin = pipe
	}

	var finalOut cappedBuffer
	finalOut.limit = MaxOutputBytes
	cmds[len(cmds)-1].Stdout = &finalOut

	// Start all stages.
	for _, c := range cmds {
		if err := c.Start(); err != nil {
			// Best effort: kill anything already started.
			for _, started := range cmds {
				if started.Process != nil {
					_ = started.Process.Kill()
				}
			}
			return Result{ExitCode: 1, Stderr: []byte("rrsh: pipeline start failed: " + err.Error() + "\n")}
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

	res := Result{
		Stdout: finalOut.Bytes(),
	}
	var mergedErr bytes.Buffer
	truncated := finalOut.truncated
	for i := range stderrs {
		mergedErr.Write(stderrs[i].Bytes())
		if stderrs[i].truncated {
			truncated = true
		}
	}
	res.Stderr = mergedErr.Bytes()
	res.Truncated = truncated

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.ExitCode = TimeoutExitCode
		res.TimedOut = true
		return res
	}
	if lastErr != nil {
		var exitErr *exec.ExitError
		if errors.As(lastErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res
		}
		res.ExitCode = 1
		return res
	}
	return res
}

// cappedBuffer is a Writer that stops accepting bytes after limit; further
// writes succeed (no short-write error) but the bytes are dropped and the
// truncated flag is set.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.truncated {
		return len(p), nil
	}
	remaining := c.limit - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte { return c.buf.Bytes() }

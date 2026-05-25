// Package exec runs single commands or native Go pipelines, with
// bounded output, env scrubbing, and process-group lifecycle control.
package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	osexec "os/exec"
	"syscall"
	"time"

	"github.com/binwiederhier/rrsh/util"
)

// killGracePeriod lets SIGKILL reach the whole process group before
// Wait returns, so grandchildren can't outlive the parent's deadline.
const killGracePeriod = 100 * time.Millisecond

// defaultChildEnv pins a minimal env so sshd's AcceptEnv can't
// influence allowlisted utilities.
var defaultChildEnv []string

func init() {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "rrsh"
	}
	defaultChildEnv = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + home,
		"LANG=C.UTF-8",
		"USER=" + user,
	}
}

// Execute runs a single command with optional stdin. Thin wrapper over
// ExecutePipeline so both shapes share one execution path. A zero
// timeout uses defaultTimeout. Empty command returns ExitCode 1.
func Execute(command []string, timeout time.Duration, stdin io.Reader) *Result {
	if len(command) == 0 {
		return &Result{ExitCode: 1, Stderr: []byte("rrsh: empty command\n")}
	}
	return ExecutePipeline([]*Stage{{Command: command, Timeout: timeout, Stdin: stdin}})
}

// ExecutePipeline runs N stages with stage i's stdout wired to stage
// i+1's stdin. The single pipeline deadline is the max of the explicit
// per-stage timeouts, or defaultTimeout if every stage's Timeout is 0.
// An explicit timeout always wins, even if shorter than defaultTimeout
// (so callers asking for a tight bound get it). Only the last stage's
// stdout is returned; stderr from every stage is merged. Exit code is
// the last stage's exit. Callers must ensure each Stage.Command is
// non-empty.
func ExecutePipeline(stages []*Stage) *Result {
	if len(stages) == 0 {
		return &Result{ExitCode: 1, Stderr: []byte("rrsh: empty pipeline\n")}
	}

	// Pipeline deadline = max of explicit per-stage timeouts, or
	// defaultTimeout when none is explicit.
	var timeout time.Duration
	for _, s := range stages {
		if s.Timeout > timeout {
			timeout = s.Timeout
		}
	}
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Make stderr buffers for each stage
	cmds := make([]*osexec.Cmd, len(stages))
	stderrs := make([]*util.CappedBuffer, len(stages))
	for i, s := range stages {
		cmds[i] = newCmd(ctx, s.Command[0], s.Command[1:]...)
		stderrs[i] = util.NewCappedBuffer(maxOutputBytes)
		cmds[i].Stderr = stderrs[i]
	}

	// Connect stdin to first command
	if stages[0].Stdin != nil {
		cmds[0].Stdin = stages[0].Stdin
	}

	// Connect stdout[i] to stdin[i+1] (cmd1 | cmd2 | ...)
	for i := 0; i < len(cmds)-1; i++ {
		pipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return &Result{ExitCode: 1, Stderr: []byte("rrsh: pipeline setup failed\n")}
		}
		cmds[i+1].Stdin = pipe
	}

	// Capture the last command's stdout
	finalOut := util.NewCappedBuffer(maxOutputBytes)
	cmds[len(cmds)-1].Stdout = finalOut

	// Start each stage (Wait happens in the loop below)
	for _, c := range cmds {
		if err := c.Start(); err != nil {
			// Two-pass cleanup: kill everyone first (so no downstream
			// stage blocks on an upstream pipe), then Wait each (so
			// os/exec's stdin/stdout copy goroutines exit and fds are
			// released for the SSH connection's lifetime).
			for _, started := range cmds {
				if started.Process != nil {
					started.Process.Kill()
				}
			}
			for _, started := range cmds {
				if started.Process != nil {
					started.Wait()
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

	// Merge stderr from all stages
	var mergedErr bytes.Buffer
	truncated := finalOut.Truncated()
	for _, s := range stderrs {
		mergedErr.Write(s.Bytes())
		if s.Truncated() {
			truncated = true
		}
	}
	return finalize(ctx, &Result{
		Stdout:    finalOut.Bytes(),
		Stderr:    mergedErr.Bytes(),
		Truncated: truncated,
	}, lastErr)
}

// newCmd builds an os/exec.Cmd with rrsh's defense-in-depth defaults:
// minimal env (sshd-passed LD_PRELOAD etc. can't influence the child),
// Setpgid (own process group), and Cancel/WaitDelay (deadline kills the
// whole group, not just the direct child).
func newCmd(ctx context.Context, path string, argv ...string) *osexec.Cmd {
	c := osexec.CommandContext(ctx, path, argv...)
	c.Env = defaultChildEnv
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return os.ErrProcessDone
		}
		// Negative pid signals the whole process group.
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = killGracePeriod
	return c
}

// finalize maps ctx state + os/exec error into ExitCode/TimedOut.
func finalize(ctx context.Context, res *Result, err error) *Result {
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

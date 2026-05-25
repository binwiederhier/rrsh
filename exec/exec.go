package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// newCmd builds an os/exec.Cmd with rrsh's defense-in-depth defaults:
// minimal env (sshd-passed LD_PRELOAD etc. can't influence the child),
// Setpgid (own process group), and Cancel/WaitDelay (deadline kills the
// whole group, not just the direct child).
func newCmd(ctx context.Context, path string, argv ...string) *osexec.Cmd {
	c := osexec.CommandContext(ctx, path, argv...)
	c.Env = defaultEnv()
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

// defaultEnv pins a minimal env for children. Inheriting rrsh's full
// env would let an SSH client influence allowlisted utilities via vars
// sshd's AcceptEnv accepts.
func defaultEnv() []string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "rrsh"
	}
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + home,
		"LANG=C.UTF-8",
		"USER=" + user,
	}
}

// Execute runs a single command with optional stdin. command[0] is
// the binary path; command[1:] is argv. A zero timeout uses
// defaultTimeout. ExitCode is the child's exit, timeoutExitCode on
// deadline, or 1 on fork/exec errors (or empty command).
func Execute(command []string, timeout time.Duration, stdin io.Reader) *Result {
	if len(command) == 0 {
		return &Result{ExitCode: 1, Stderr: []byte("rrsh: empty command\n")}
	}
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	stdout := util.NewCappedBuffer(maxOutputBytes)
	stderr := util.NewCappedBuffer(maxOutputBytes)

	c := newCmd(ctx, command[0], command[1:]...)
	c.Stdout = stdout
	c.Stderr = stderr
	if stdin != nil {
		c.Stdin = stdin
	}

	err := c.Run()
	return finalize(ctx, &Result{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Truncated: stdout.Truncated() || stderr.Truncated(),
	}, err)
}

// finalize maps ctx state + os/exec error into ExitCode/TimedOut.
// Shared by Execute and ExecutePipeline.
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

// ExecutePipeline runs N stages with stage i's stdout wired to stage
// i+1's stdin. All stages share one deadline (max of defaultTimeout and
// any per-stage timeout). Only the last stage's stdout is returned; stderr
// from every stage is merged. Exit code is the last stage's exit.
func ExecutePipeline(stages []*Stage) *Result {
	if len(stages) == 0 {
		return &Result{ExitCode: 1, Stderr: []byte("rrsh: empty pipeline\n")}
	} else if len(stages) == 1 {
		s := stages[0]
		return Execute(s.Command, s.Timeout, s.Stdin)
	}

	// Update stages with timeout
	timeout := defaultTimeout
	for _, s := range stages {
		if s.Timeout > timeout {
			timeout = s.Timeout
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Make stderr buffers for each stage
	cmds := make([]*osexec.Cmd, len(stages))
	stderrs := make([]*util.CappedBuffer, len(stages))
	for i, s := range stages {
		if len(s.Command) == 0 {
			return &Result{ExitCode: 1, Stderr: []byte(fmt.Sprintf("rrsh: pipeline stage %d has empty command\n", i))}
		}
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

	// Get a buffer to the last command's stdout
	finalOut := util.NewCappedBuffer(maxOutputBytes)
	cmds[len(cmds)-1].Stdout = finalOut

	// Start them all and wait
	for _, c := range cmds {
		if err := c.Start(); err != nil {
			// Reap killed children so os/exec's stdin/stdout copy
			// goroutines exit - otherwise fds and goroutines leak for
			// the lifetime of the SSH connection.
			for _, started := range cmds {
				if started.Process != nil {
					_ = started.Process.Kill()
				}
			}
			for _, started := range cmds {
				if started.Process != nil {
					_ = started.Wait()
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

	// Collect errors
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

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

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/util"
)

// killGracePeriod gives the kernel a moment to deliver SIGKILL to every
// member of the process group before Wait returns, so that fast-spawning
// grandchildren can't outlive their parent's deadline.
const killGracePeriod = 100 * time.Millisecond

// newCmd builds an os/exec.Cmd configured with rrsh's three
// defense-in-depth measures:
//   - explicit minimal env (LD_PRELOAD, PYTHONSTARTUP, etc. from sshd
//     cannot influence allowlisted children)
//   - Setpgid so children form their own process group
//   - Cancel/WaitDelay so context-deadline kills the whole group,
//     not just the direct child (otherwise grandchildren leak past
//     the rule's timeout)
func newCmd(ctx context.Context, path string, argv ...string) *osexec.Cmd {
	c := osexec.CommandContext(ctx, path, argv...)
	c.Env = defaultEnv()
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return os.ErrProcessDone
		}
		// Negative pid signals the entire process group, reaching
		// children the direct child forked off.
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = killGracePeriod
	return c
}

// defaultEnv returns the minimal environment passed to every child.
// Inheriting rrsh's full environment would let an authenticated SSH
// client influence allowlisted utilities via env vars sshd happens to
// accept (LC_*, LANG, ...); pinning a small list closes that path.
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

	c := newCmd(ctx, path, argv...)
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
	} else if len(stages) == 1 {
		s := stages[0]
		return Execute(s.Path, s.Argv, s.Rule, s.Stdin)
	}

	// Update stages with timeout
	timeout := defaultTimeout
	for _, s := range stages {
		if s.Rule != nil && s.Rule.Timeout > timeout {
			timeout = s.Rule.Timeout
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Make stderr buffers for each stage
	cmds := make([]*osexec.Cmd, len(stages))
	stderrs := make([]*util.CappedBuffer, len(stages))
	for i, s := range stages {
		cmds[i] = newCmd(ctx, s.Path, s.Argv...)
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
			// Kill anything already started, then reap so the os/exec
			// goroutines copying stdin/stdout for the killed children
			// can finish - otherwise fds and goroutines leak for the
			// lifetime of the SSH connection.
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
	return finalize(&Result{
		Stdout:    finalOut.Bytes(),
		Stderr:    mergedErr.Bytes(),
		Truncated: truncated,
	}, ctx, lastErr)
}

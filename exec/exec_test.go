package exec

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestExecute_Success(t *testing.T) {
	t.Parallel()
	res := Execute([]string{"/bin/echo", "hello"}, 0, nil)
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
	if !bytes.Equal(bytes.TrimSpace(res.Stdout), []byte("hello")) {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello")
	}
}

func TestExecute_ExitCode(t *testing.T) {
	t.Parallel()
	res := Execute([]string{"/bin/false"}, 0, nil)
	if res.ExitCode == 0 {
		t.Error("expected non-zero exit code from /bin/false")
	}
}

func TestExecute_EmptyCommand(t *testing.T) {
	t.Parallel()
	res := Execute(nil, 0, nil)
	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", res.ExitCode)
	}
}

// Per-stage timeout still applies; defaultTimeout is irrelevant here
// because the caller asked for a tighter bound.
func TestExecute_PerStageTimeout(t *testing.T) {
	t.Parallel()
	start := time.Now()
	res := Execute([]string{"/bin/sleep", "10"}, 100*time.Millisecond, nil)
	elapsed := time.Since(start)

	if res.ExitCode != timeoutExitCode {
		t.Errorf("exit code = %d, want %d (timeout)", res.ExitCode, timeoutExitCode)
	}
	if !res.TimedOut {
		t.Error("TimedOut should be true")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, per-stage timeout should have kicked in", elapsed)
	}
}

// Argv with embedded spaces - the structural fix that the rewrite enables.
// Today's string-based path would have split "a b" into ["\"a", "b\""] (or
// just split on whitespace, losing the grouping). With argv arrays, a
// single string with an internal space stays a single arg.
func TestExecute_ArgWithEmbeddedSpace(t *testing.T) {
	t.Parallel()
	res := Execute([]string{"/bin/echo", "a b", "c"}, 0, nil)
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	// /bin/echo prints args joined by single spaces, so the output should
	// reproduce them verbatim: "a b c\n".
	if string(bytes.TrimSpace(res.Stdout)) != "a b c" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "a b c")
	}
}

func TestExecute_Stdin(t *testing.T) {
	t.Parallel()
	res := Execute([]string{"/bin/cat"}, 0, strings.NewReader("hello stdin"))
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	if string(res.Stdout) != "hello stdin" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello stdin")
	}
}

func TestExecute_StderrCaptured(t *testing.T) {
	t.Parallel()
	// /bin/sh -c "echo err >&2" would require a shell; instead use a
	// command that's guaranteed to write to stderr: ls of a missing file.
	res := Execute([]string{"/bin/ls", "/nonexistent-path-rrsh-test"}, 0, nil)
	if res.ExitCode == 0 {
		t.Error("ls of missing path should fail")
	}
	if len(res.Stderr) == 0 {
		t.Error("expected stderr to be captured")
	}
}

func TestExecutePipeline_Success(t *testing.T) {
	t.Parallel()
	res := ExecutePipeline([]*Stage{
		{Command: []string{"/bin/echo", "hello pipeline"}},
		{Command: []string{"/bin/cat"}},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if string(bytes.TrimSpace(res.Stdout)) != "hello pipeline" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello pipeline")
	}
}

func TestExecutePipeline_LastStageExitWins(t *testing.T) {
	t.Parallel()
	// Even though the first stage succeeds, the last stage exits non-zero.
	res := ExecutePipeline([]*Stage{
		{Command: []string{"/bin/echo", "x"}},
		{Command: []string{"/bin/false"}},
	})
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit, got %d", res.ExitCode)
	}
}

func TestExecutePipeline_Stdin(t *testing.T) {
	t.Parallel()
	res := ExecutePipeline([]*Stage{
		{Command: []string{"/bin/cat"}, Stdin: strings.NewReader("piped in\n")},
		{Command: []string{"/bin/cat"}},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if string(res.Stdout) != "piped in\n" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestExecutePipeline_SingleStageEquivalentToExecute(t *testing.T) {
	t.Parallel()
	res := ExecutePipeline([]*Stage{
		{Command: []string{"/bin/echo", "solo"}},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	if string(bytes.TrimSpace(res.Stdout)) != "solo" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

// 1 MiB of output should fit cleanly under maxOutputBytes (10 MiB) and
// surface as Truncated=false.
func TestExecute_LargeOutputFits(t *testing.T) {
	t.Parallel()
	res := Execute([]string{"/usr/bin/head", "-c", "1048576", "/dev/zero"}, 0, nil)
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	if len(res.Stdout) != 1024*1024 {
		t.Errorf("stdout length = %d, want %d", len(res.Stdout), 1024*1024)
	}
	if res.Truncated {
		t.Errorf("Truncated should be false for output under the cap")
	}
}

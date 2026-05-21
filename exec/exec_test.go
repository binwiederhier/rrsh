package exec

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/binwiederhier/rrsh/config"
)

func TestExecute_Success(t *testing.T) {
	t.Parallel()
	rule := &config.CommandRule{}
	res := Execute("/bin/echo", []string{"hello"}, rule, nil)
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
	if !bytes.Equal(bytes.TrimSpace(res.Stdout), []byte("hello")) {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello")
	}
}

func TestExecute_ExitCode(t *testing.T) {
	t.Parallel()
	rule := &config.CommandRule{}
	res := Execute("/bin/false", nil, rule, nil)
	if res.ExitCode == 0 {
		t.Error("expected non-zero exit code from /bin/false")
	}
}

// Per-rule timeout still applies; defaultTimeout is irrelevant here
// because the rule asked for a tighter bound.
func TestExecute_PerRuleTimeout(t *testing.T) {
	t.Parallel()
	rule := &config.CommandRule{
		Timeout: 100 * time.Millisecond,
	}
	start := time.Now()
	res := Execute("/bin/sleep", []string{"10"}, rule, nil)
	elapsed := time.Since(start)

	if res.ExitCode != timeoutExitCode {
		t.Errorf("exit code = %d, want %d (timeout)", res.ExitCode, timeoutExitCode)
	}
	if !res.TimedOut {
		t.Error("TimedOut should be true")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, per-rule timeout should have kicked in", elapsed)
	}
}

// Argv with embedded spaces - the structural fix that the rewrite enables.
// Today's string-based path would have split "a b" into ["\"a", "b\""] (or
// just split on whitespace, losing the grouping). With argv arrays, a
// single string with an internal space stays a single arg.
func TestExecute_ArgWithEmbeddedSpace(t *testing.T) {
	t.Parallel()
	rule := &config.CommandRule{}
	res := Execute("/bin/echo", []string{"a b", "c"}, rule, nil)
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
	rule := &config.CommandRule{}
	res := Execute("/bin/cat", nil, rule, strings.NewReader("hello stdin"))
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
	rule := &config.CommandRule{}
	res := Execute("/bin/ls", []string{"/nonexistent-path-rrsh-test"}, rule, nil)
	if res.ExitCode == 0 {
		t.Error("ls of missing path should fail")
	}
	if len(res.Stderr) == 0 {
		t.Error("expected stderr to be captured")
	}
}

func TestExecutePipeline_Success(t *testing.T) {
	t.Parallel()
	echoRule := &config.CommandRule{}
	catRule := &config.CommandRule{}
	res := ExecutePipeline([]*Stage{
		&Stage{Path: "/bin/echo", Argv: []string{"hello pipeline"}, Rule: echoRule},
		&Stage{Path: "/bin/cat", Rule: catRule},
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
	echoRule := &config.CommandRule{}
	falseRule := &config.CommandRule{}
	res := ExecutePipeline([]*Stage{
		&Stage{Path: "/bin/echo", Argv: []string{"x"}, Rule: echoRule},
		&Stage{Path: "/bin/false", Rule: falseRule},
	})
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit, got %d", res.ExitCode)
	}
}

func TestExecutePipeline_Stdin(t *testing.T) {
	t.Parallel()
	catRule := &config.CommandRule{}
	res := ExecutePipeline([]*Stage{
		&Stage{Path: "/bin/cat", Rule: catRule, Stdin: strings.NewReader("piped in\n")},
		&Stage{Path: "/bin/cat", Rule: catRule},
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
	rule := &config.CommandRule{}
	res := ExecutePipeline([]*Stage{
		&Stage{Path: "/bin/echo", Argv: []string{"solo"}, Rule: rule},
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
	rule := &config.CommandRule{}
	res := Execute("/usr/bin/head", []string{"-c", "1048576", "/dev/zero"}, rule, nil)
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

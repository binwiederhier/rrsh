package executor

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/binwiederhier/rrsh/config"
)

func TestExecute_Success(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/echo"}
	res := New(10 * time.Second).Execute("/bin/echo", []string{"hello"}, rule, nil)
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
	if !bytes.Equal(bytes.TrimSpace(res.Stdout), []byte("hello")) {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello")
	}
}

func TestExecute_ExitCode(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/false"}
	res := New(10 * time.Second).Execute("/bin/false", nil, rule, nil)
	if res.ExitCode == 0 {
		t.Error("expected non-zero exit code from /bin/false")
	}
}

func TestExecute_Timeout(t *testing.T) {
	rule := &config.CommandRule{
		Path:        "/bin/sleep",
		ArgsPattern: regexp.MustCompile(`^\d+$`),
		Timeout:     100 * time.Millisecond,
	}
	res := New(10 * time.Second).Execute("/bin/sleep", []string{"10"}, rule, nil)
	if res.ExitCode != TimeoutExitCode {
		t.Errorf("exit code = %d, want %d (timeout)", res.ExitCode, TimeoutExitCode)
	}
	if !res.TimedOut {
		t.Error("TimedOut should be true")
	}
}

func TestExecute_GlobalTimeout(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/sleep"}
	res := New(100 * time.Millisecond).Execute("/bin/sleep", []string{"10"}, rule, nil)
	if res.ExitCode != TimeoutExitCode {
		t.Errorf("exit code = %d, want %d", res.ExitCode, TimeoutExitCode)
	}
}

func TestExecute_PerCommandTimeoutOverride(t *testing.T) {
	rule := &config.CommandRule{
		Path:    "/bin/sleep",
		Timeout: 100 * time.Millisecond,
	}
	start := time.Now()
	res := New(10 * time.Second).Execute("/bin/sleep", []string{"10"}, rule, nil)
	elapsed := time.Since(start)

	if res.ExitCode != TimeoutExitCode {
		t.Errorf("exit code = %d, want %d", res.ExitCode, TimeoutExitCode)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, per-command timeout should have kicked in", elapsed)
	}
}

// Argv with embedded spaces — the structural fix that the rewrite enables.
// Today's string-based path would have split "a b" into ["\"a", "b\""] (or
// just split on whitespace, losing the grouping). With argv arrays, a
// single string with an internal space stays a single arg.
func TestExecute_ArgWithEmbeddedSpace(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/echo"}
	res := New(10 * time.Second).Execute("/bin/echo", []string{"a b", "c"}, rule, nil)
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
	rule := &config.CommandRule{Path: "/bin/cat"}
	res := New(10 * time.Second).Execute("/bin/cat", nil, rule, strings.NewReader("hello stdin"))
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	if string(res.Stdout) != "hello stdin" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello stdin")
	}
}

func TestExecute_StderrCaptured(t *testing.T) {
	// /bin/sh -c "echo err >&2" would require a shell; instead use a
	// command that's guaranteed to write to stderr: ls of a missing file.
	rule := &config.CommandRule{Path: "/bin/ls"}
	res := New(10 * time.Second).Execute("/bin/ls", []string{"/nonexistent-path-rrsh-test"}, rule, nil)
	if res.ExitCode == 0 {
		t.Error("ls of missing path should fail")
	}
	if len(res.Stderr) == 0 {
		t.Error("expected stderr to be captured")
	}
}

func TestExecutePipeline_Success(t *testing.T) {
	echoRule := &config.CommandRule{Path: "/bin/echo"}
	catRule := &config.CommandRule{Path: "/bin/cat"}
	res := New(10 * time.Second).ExecutePipeline([]Stage{
		{Path: "/bin/echo", Argv: []string{"hello pipeline"}, Rule: echoRule},
		{Path: "/bin/cat", Rule: catRule},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if string(bytes.TrimSpace(res.Stdout)) != "hello pipeline" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello pipeline")
	}
}

func TestExecutePipeline_LastStageExitWins(t *testing.T) {
	// Even though the first stage succeeds, the last stage exits non-zero.
	echoRule := &config.CommandRule{Path: "/bin/echo"}
	falseRule := &config.CommandRule{Path: "/bin/false"}
	res := New(10 * time.Second).ExecutePipeline([]Stage{
		{Path: "/bin/echo", Argv: []string{"x"}, Rule: echoRule},
		{Path: "/bin/false", Rule: falseRule},
	})
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit, got %d", res.ExitCode)
	}
}

func TestExecutePipeline_Timeout(t *testing.T) {
	sleepRule := &config.CommandRule{Path: "/bin/sleep"}
	catRule := &config.CommandRule{Path: "/bin/cat"}
	res := New(100 * time.Millisecond).ExecutePipeline([]Stage{
		{Path: "/bin/sleep", Argv: []string{"10"}, Rule: sleepRule},
		{Path: "/bin/cat", Rule: catRule},
	})
	if res.ExitCode != TimeoutExitCode {
		t.Errorf("exit code = %d, want %d", res.ExitCode, TimeoutExitCode)
	}
	if !res.TimedOut {
		t.Error("TimedOut should be true")
	}
}

func TestExecutePipeline_Stdin(t *testing.T) {
	catRule := &config.CommandRule{Path: "/bin/cat"}
	res := New(10 * time.Second).ExecutePipeline([]Stage{
		{Path: "/bin/cat", Rule: catRule, Stdin: strings.NewReader("piped in\n")},
		{Path: "/bin/cat", Rule: catRule},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if string(res.Stdout) != "piped in\n" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestExecutePipeline_SingleStageEquivalentToExecute(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/echo"}
	res := New(10 * time.Second).ExecutePipeline([]Stage{
		{Path: "/bin/echo", Argv: []string{"solo"}, Rule: rule},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	if string(bytes.TrimSpace(res.Stdout)) != "solo" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestExecute_Truncation(t *testing.T) {
	// Use yes piped through head — but ExecutePipeline isn't ready for
	// large output, so use a small limit by running yes for a tiny timeout
	// against a small cap. Instead, write directly to a cappedBuffer to
	// exercise the truncation logic.
	cb := cappedBuffer{limit: 4}
	n, _ := cb.Write([]byte("12345678"))
	if n != 8 {
		t.Errorf("Write returned %d, want 8 (cappedBuffer reports full write)", n)
	}
	if !cb.truncated {
		t.Error("expected truncated flag")
	}
	if string(cb.Bytes()) != "1234" {
		t.Errorf("Bytes() = %q, want %q", cb.Bytes(), "1234")
	}
	// Subsequent writes also accepted (no error) but dropped.
	n, _ = cb.Write([]byte("more"))
	if n != 4 {
		t.Errorf("post-truncate Write returned %d, want 4", n)
	}
	if string(cb.Bytes()) != "1234" {
		t.Errorf("post-truncate Bytes() = %q", cb.Bytes())
	}
}

package main

import (
	"regexp"
	"testing"
	"time"
)

func TestExecute_Success(t *testing.T) {
	rule := &CommandRule{Path: "/bin/echo"}
	code := Execute("/bin/echo hello", rule, 10*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestExecute_ExitCode(t *testing.T) {
	rule := &CommandRule{Path: "/bin/false"}
	code := Execute("/bin/false", rule, 10*time.Second)
	if code == 0 {
		t.Error("expected non-zero exit code from /bin/false")
	}
}

func TestExecute_Timeout(t *testing.T) {
	rule := &CommandRule{
		Path:       "/bin/sleep",
		ArgsPattern: regexp.MustCompile(`^\d+$`),
		Timeout:    100 * time.Millisecond,
	}
	code := Execute("/bin/sleep 10", rule, 10*time.Second)
	if code != 124 {
		t.Errorf("exit code = %d, want 124 (timeout)", code)
	}
}

func TestExecute_GlobalTimeout(t *testing.T) {
	rule := &CommandRule{Path: "/bin/sleep"}
	code := Execute("/bin/sleep 10", rule, 100*time.Millisecond)
	if code != 124 {
		t.Errorf("exit code = %d, want 124 (timeout)", code)
	}
}

func TestExecute_PerCommandTimeoutOverride(t *testing.T) {
	rule := &CommandRule{
		Path:    "/bin/sleep",
		Timeout: 100 * time.Millisecond,
	}
	// Per-command timeout (100ms) should override global (10s)
	start := time.Now()
	code := Execute("/bin/sleep 10", rule, 10*time.Second)
	elapsed := time.Since(start)

	if code != 124 {
		t.Errorf("exit code = %d, want 124", code)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, per-command timeout should have kicked in", elapsed)
	}
}

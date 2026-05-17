package executor

import (
	"regexp"
	"testing"
	"time"

	"github.com/pheckel/noshell/config"
)

func TestExecute_Success(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/echo"}
	code := New(10 * time.Second).Execute("/bin/echo hello", rule)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestExecute_ExitCode(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/false"}
	code := New(10 * time.Second).Execute("/bin/false", rule)
	if code == 0 {
		t.Error("expected non-zero exit code from /bin/false")
	}
}

func TestExecute_Timeout(t *testing.T) {
	rule := &config.CommandRule{
		Path:        "/bin/sleep",
		ArgsPattern: regexp.MustCompile(`^\d+$`),
		Timeout:     100 * time.Millisecond,
	}
	code := New(10 * time.Second).Execute("/bin/sleep 10", rule)
	if code != 124 {
		t.Errorf("exit code = %d, want 124 (timeout)", code)
	}
}

func TestExecute_GlobalTimeout(t *testing.T) {
	rule := &config.CommandRule{Path: "/bin/sleep"}
	code := New(100 * time.Millisecond).Execute("/bin/sleep 10", rule)
	if code != 124 {
		t.Errorf("exit code = %d, want 124 (timeout)", code)
	}
}

func TestExecute_PerCommandTimeoutOverride(t *testing.T) {
	rule := &config.CommandRule{
		Path:    "/bin/sleep",
		Timeout: 100 * time.Millisecond,
	}
	// Per-command timeout (100ms) should override global (10s)
	start := time.Now()
	code := New(10 * time.Second).Execute("/bin/sleep 10", rule)
	elapsed := time.Since(start)

	if code != 124 {
		t.Errorf("exit code = %d, want 124", code)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, per-command timeout should have kicked in", elapsed)
	}
}

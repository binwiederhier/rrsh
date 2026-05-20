package cmd

import (
	"os"
	"testing"
)

// TestIsTerminal_PipeIsNotATerminal verifies the TTY detection used to
// decide whether to print the shell-mode rejection vs entering the
// JSON-RPC server loop. A pipe from io.Pipe is the closest portable
// proxy for what sshd hands us when the client passes `-T` or pipes
// stdin in.
func TestIsTerminal_PipeIsNotATerminal(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()
	defer pw.Close()

	if isTerminal(pr) {
		t.Error("a pipe should not be detected as a terminal")
	}
}

// TestIsTerminal_RegularFileIsNotATerminal confirms a plain file also
// reports false. Together with the pipe case this is enough to ensure
// the rejection-on-TTY logic doesn't trigger when JSON-RPC is being
// piped or redirected.
func TestIsTerminal_RegularFileIsNotATerminal(t *testing.T) {
	tmp, err := os.CreateTemp("", "rrsh-isterm-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if isTerminal(tmp) {
		t.Error("a regular file should not be detected as a terminal")
	}
}

// Ensure isTerminal handles a nil-but-typed *os.File safely. Defensive:
// no caller passes nil today, but this guards a future regression.
func TestIsTerminal_StatErrorReturnsFalse(t *testing.T) {
	// Close a file first, then call. Stat on a closed file returns an
	// error on most platforms; isTerminal should return false rather
	// than panic.
	pr, pw, _ := os.Pipe()
	pw.Close()
	pr.Close()

	if got := isTerminal(pr); got {
		t.Error("closed file should not be detected as terminal")
	}
}

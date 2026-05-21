package exec

import (
	"io"
	"time"

	"github.com/binwiederhier/rrsh/config"
)

const (
	// maxOutputBytes caps captured stdout and stderr per stream. Overflow
	// is silently dropped and Result.Truncated is set.
	maxOutputBytes = 10 * 1024 * 1024 // 10 MB
	// timeoutExitCode is returned when the context deadline fires.
	timeoutExitCode = 124
	// defaultTimeout applies when the rule does not set its own.
	// Intentionally fixed (not config-tunable) so a misconfigured global
	// can't let runaway commands hold the JSON-RPC channel hostage.
	defaultTimeout = 30 * time.Second
)

// Result is the structured outcome of running a command or pipeline.
type Result struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	TimedOut  bool
	Truncated bool
}

// Stage is one segment of a pipeline. Stdin is only honored on stage 0.
type Stage struct {
	Path  string
	Argv  []string
	Rule  *config.CommandRule
	Stdin io.Reader
}

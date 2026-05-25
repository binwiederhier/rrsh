package exec

import (
	"io"
	"time"
)

const (
	// maxOutputBytes caps captured stdout and stderr per stream. Overflow
	// is silently dropped and Result.Truncated is set.
	maxOutputBytes = 10 * 1024 * 1024 // 10 MB
	// timeoutExitCode is returned when the context deadline fires.
	timeoutExitCode = 124
	// defaultTimeout applies when the caller passes 0.
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

// Stage is one segment of a pipeline. Command[0] is the binary path
// and the rest are argv. Timeout of 0 means defaultTimeout.
// Stdin is only honored on stage 0.
type Stage struct {
	Command []string
	Timeout time.Duration
	Stdin   io.Reader
}

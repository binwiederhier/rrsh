package exec

import (
	"io"
	"time"

	"github.com/binwiederhier/rrsh/config"
)

// maxOutputBytes is the per-stream (stdout/stderr) cap on captured output.
// Output beyond this is silently dropped and the Result is marked Truncated.
const maxOutputBytes = 10 * 1024 * 1024 // 10 MB

// timeoutExitCode is returned when the context deadline fires before the
// command (or pipeline) finishes. Matches shell convention.
const timeoutExitCode = 124

// defaultTimeout is the wall-clock deadline applied to a command whose
// matched rule does not specify its own Timeout. It is intentionally a
// fixed package constant rather than a tunable: letting operators raise
// it globally would silently let runaway commands hold the JSON-RPC
// channel hostage. Rules that need more or less override per-command
// via CommandRule.Timeout.
const defaultTimeout = 30 * time.Second

// Result is the structured outcome of running a command or pipeline.
type Result struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	TimedOut  bool
	Truncated bool
}

// Stage is one segment of a pipeline.
type Stage struct {
	Path string
	Argv []string
	Rule *config.CommandRule
	// Stdin, if non-nil, is wired to the first stage's stdin. Ignored on
	// stages other than index 0.
	Stdin io.Reader
}

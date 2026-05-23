package server

import "encoding/json"

// JSON-RPC 2.0 error codes. errDenied is rrsh's own (reserved
// app-code range -32000..-32099).
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errDenied         = -32000
)

// jsonrpcRequest: ID is RawMessage so we can echo it back unchanged
// (any of number/string/null are valid IDs).
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// jsonrpcResponse: exactly one of Result or Error is set.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError       `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// listCommandsResult: host-specific Instructions plus the full Commands
// allowlist, so the AI gets everything in one round-trip.
type listCommandsResult struct {
	Instructions string          `json:"instructions,omitempty"`
	Commands     []*commandEntry `json:"commands"`
}

// commandEntry: one list_commands.commands element. Command[0] matches
// the binary path; [1..N-1] match argv 1-for-1.
type commandEntry struct {
	Command     []string `json:"command"`
	As          []string `json:"as"`
	Description string   `json:"description,omitempty"`
	TimeoutSecs float64  `json:"timeout_seconds,omitempty"`
}

// runCommandParams is the request body of the `run_command` method.
type runCommandParams struct {
	Argv  []string `json:"argv"`
	As    string   `json:"as,omitempty"`
	Stdin string   `json:"stdin,omitempty"`
}

// runPipelineParams: Pipeline is a value slice (not []*runStep) so a
// JSON `null` element decodes to a zero-value runStep that the
// empty-argv check rejects, rather than a nil pointer that would panic.
type runPipelineParams struct {
	Pipeline []runStep `json:"pipeline"`
	Stdin    string    `json:"stdin,omitempty"`
}

// runStep is one stage of a pipeline.
type runStep struct {
	Argv []string `json:"argv"`
	As   string   `json:"as,omitempty"`
}

// runResult is the response body of both `run_command` and `run_pipeline`.
type runResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Exit      int    `json:"exit"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

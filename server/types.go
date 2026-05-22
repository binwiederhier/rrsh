package server

import "encoding/json"

// JSON-RPC 2.0 error codes (subset). errDenied is rrsh's own
// application code, reserved range -32000..-32099.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errDenied         = -32000
)

// request is one JSON-RPC 2.0 request. ID is RawMessage so we echo it
// back unchanged (numbers, strings, and null are all valid IDs).
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// response is one JSON-RPC 2.0 response. Exactly one of Result and
// Error is set.
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

// helloResult is the response body of the `hello` method. Instructions
// carries host-specific guidance the AI reads on first contact;
// Commands is the full allowlist (one round-trip self-description).
type helloResult struct {
	Instructions string          `json:"instructions,omitempty"`
	Commands     []*commandEntry `json:"commands"`
}

// commandEntry is one element of hello's commands array. Command is
// the rule's source regex patterns: index 0 matches the binary path,
// indices 1..N-1 match argv 1-for-1.
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

// runPipelineParams is the request body of the `run_pipeline` method.
// Pipeline is a value slice (not []*runStep) so a JSON `null` element
// decodes as a zero-value runStep that the per-stage empty-argv check
// rejects cleanly, rather than as a nil pointer that would panic on
// the first field access.
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

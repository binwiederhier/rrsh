// Package server implements rrsh's JSON-RPC 2.0 server over stdio.
// Three methods are exposed: hello, list, run. Server-side refusals
// (matcher denials, oversize requests, elevation disabled) use the
// JSON-RPC error envelope; the child process's own exit code lives in
// the run result's `exit` field.
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
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// response is one JSON-RPC 2.0 response. Exactly one of Result and
// Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// helloResult is the response body of the `hello` method.
// Instructions carries host-specific guidance the AI reads on first
// contact.
type helloResult struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Instructions string `json:"instructions,omitempty"`
}

// listResult is the response body of the `list` method.
type listResult struct {
	Commands []commandEntry `json:"commands"`
}

// commandEntry is one element of `list`'s commands array. Command is
// the rule's source regex patterns: index 0 matches the binary path,
// indices 1..N-1 match argv 1-for-1.
type commandEntry struct {
	Command     []string `json:"command"`
	As          []string `json:"as"`
	Description string   `json:"description,omitempty"`
	TimeoutSecs float64  `json:"timeout_seconds,omitempty"`
}

// runParams is the request body of the `run` method. Exactly one of
// Argv and Pipeline must be set.
type runParams struct {
	Argv     []string  `json:"argv,omitempty"`
	Pipeline []runStep `json:"pipeline,omitempty"`
	As       string    `json:"as,omitempty"`
	Stdin    string    `json:"stdin,omitempty"`
}

// runStep is one stage of a pipeline.
type runStep struct {
	Argv []string `json:"argv"`
	As   string   `json:"as,omitempty"`
}

// runResult is the response body of the `run` method.
type runResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Exit      int    `json:"exit"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

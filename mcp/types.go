// Package mcp implements a minimal MCP-compatible JSON-RPC 2.0 server over
// stdio for rrsh. It exposes two tools — list_commands and run_command —
// backed by the existing matcher/exec packages.
//
// The protocol surface is intentionally tiny: initialize, tools/list,
// tools/call. No notifications, no resources, no prompts. The trust
// boundary is the JSON decoder (stdlib, DisallowUnknownFields) plus the
// matcher.
package mcp

import "encoding/json"

// protocolVersion is the MCP wire protocol version this server speaks.
//
// This is *not* the rrsh binary version (that's `serverInfo.version`,
// populated from main's ldflags). The MCP spec defines a fixed set of
// allowed values for this field — currently date strings like
// "2025-03-26" — and clients match against that whitelist. Reporting an
// arbitrary version (e.g. "v0.3.1") would make every MCP-compliant
// client reject the handshake.
//
// Bump this when adopting a newer MCP spec, not when releasing rrsh.
const protocolVersion = "2025-03-26"

// JSON-RPC 2.0 error codes (subset).
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// request is one JSON-RPC 2.0 request. ID is left as RawMessage so we can
// echo it back unchanged (numbers, strings, and null are all valid IDs).
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// response is one JSON-RPC 2.0 response. Exactly one of Result and Error
// is set.
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

// initializeResult is the response body for the `initialize` method.
// Instructions carries host-specific guidance — the AI receives this on
// first contact, before it knows anything else about the server.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type serverCapabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct{}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// toolDef is one entry in the `tools/list` response.
type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// toolsListResult is the response body for `tools/list`.
type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

// toolsCallParams is the request body for `tools/call`.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolsCallResult is the response body for `tools/call`.
type toolsCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// contentBlock is one element of a tool's output. Only "text" is used by
// this server.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// runCommandArgs is the JSON body of the run_command tool's `arguments`.
// Exactly one of Argv and Pipeline must be set.
type runCommandArgs struct {
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

// runCommandOutput is the structured JSON the run_command tool writes into
// its content[0].text. The wrapping is plain JSON (not MCP structured
// content) so clients without schema awareness can still parse it.
type runCommandOutput struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Exit      int    `json:"exit"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// commandEntry is one element of the list_commands tool's output.
// Command is the rule's source patterns: index 0 is the path regex,
// indices 1..N-1 are per-argv-element regexes.
type commandEntry struct {
	Command     []string `json:"command"`
	As          []string `json:"as"`
	Description string   `json:"description,omitempty"`
	TimeoutSecs float64  `json:"timeout_seconds,omitempty"`
}
